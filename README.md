# livetv-server

[![Build](https://github.com/minshurui/livetv-server/actions/workflows/build-image.yml/badge.svg)](https://github.com/minshurui/livetv-server/actions/workflows/build-image.yml)
[![Docker Image](https://img.shields.io/docker/v/minshurui/livetv-allinone?label=Docker%20Hub)](https://hub.docker.com/r/minshurui/livetv-allinone)

把“当前正在直播的虎牙/斗鱼房间”和“经过检测的 IPTV 电视源”放进同一个 M3U。项目会定时刷新房间、重新解析过期地址、检测斗鱼真实拉流、过滤明显录播/VOD，并在新结果异常时保留上一次可用列表。

Docker 镜像支持 `linux/amd64` 和 `linux/arm64`。部署完成后，播放器只需要添加一个地址：

```text
http://服务器IP或域名:8081/allinone.m3u
```

> 第一次使用建议先读 [Docker/群晖部署白皮书](docs/docker.md)。里面包含准备工作、复制即用的 Compose、群晖图形界面、端口修改、首次启动、升级、备份和回滚。

## 先弄懂这四件事

1. `8081` 提供最终 M3U，但 M3U 里的虎牙和斗鱼会继续访问 `19090`，所以播放器还必须能访问该流端口。`19091` 只保留给旧版虎牙直通链接回滚。
2. `8080` 是 IPTV 管理页面，不是播放地址，也不建议直接暴露到公网。
3. 首次启动不会立刻出现全部频道。虎牙/斗鱼通常先出现，IPTV 下载、测速和过滤可能需要几分钟到几十分钟。
4. 项目能排除失效地址、假响应和明显文件型录播，但无法仅靠网络协议百分之百识别“持续循环且一直生成新分片”的视频。

## 选择部署方式

| 你的设备 | 建议方式 | 最终入口 | 文档 |
|---|---|---|---|
| Debian / Ubuntu / 普通 Linux | Docker Compose | `:8081/allinone.m3u` | [Docker 部署](docs/docker.md#方案-a普通-linux一键部署) |
| 群晖 DSM 7 | Container Manager 项目 | `:8081/allinone.m3u` | [群晖部署](docs/docker.md#方案-b群晖-container-manager) |
| OpenWrt / iStoreOS | Docker Compose，先检查端口和磁盘 | `:8081/allinone.m3u` | [OpenWrt 注意事项](docs/docker.md#方案-copenwrt--istoreos) |
| Android / Termux | 本机编译精简模式 | `:35455/allinone.m3u` | [Termux 部署](docs/termux.md) |

Termux 模式不包含完整 `iptv-api` 测速栈；想要最完整功能，优先使用 Docker。

## 五分钟 Docker 部署

下面这份 Compose 不需要下载源码。先创建一个空目录：

```bash
mkdir -p /opt/livetv
cd /opt/livetv
nano compose.yml
```

粘贴：

```yaml
services:
  livetv:
    image: minshurui/livetv-allinone:latest
    container_name: livetv
    restart: unless-stopped
    init: true
    ports:
      - "8081:8081"   # 最终 M3U
      - "19090:19090" # 虎牙/斗鱼 Go 续流代理
      - "19091:19091" # 可选：旧版虎牙 Python 直通兼容
      - "8080:8080"   # 可选：IPTV 管理页面
    environment:
      TZ: Asia/Shanghai
      PUBLIC_HOST: ""
      HUYA_CDN: AL
      HUYA_CODEC: "264"
      HUYA_MAX_RATIO: "2000" # 最高约 2000K；0=原画
      IPTV_REJECT_VOD: "1"
      IPTV_MIN_CHANNELS: "20"
    volumes:
      - ./data:/data
    security_opt:
      - no-new-privileges:true
    logging:
      options:
        max-size: "10m"
        max-file: "3"
```

启动：

```bash
docker compose pull
docker compose up -d
docker compose ps
docker logs --tail=100 -f livetv
```

按 `Ctrl+C` 只是退出日志，不会停止容器。

## 部署后怎样确认可用

先在服务器本机执行：

```bash
curl -fsS http://127.0.0.1:8081/healthz && echo
curl -fsS http://127.0.0.1:8081/allinone.m3u | sed -n '1,8p'
docker inspect --format '{{.State.Health.Status}}' livetv
```

以上只能确认服务已经启动。要验证虎牙真实播放、传输速度和最长停顿，请在仓库目录运行：

```bash
python3 tests/live_stream_smoke.py \
  --playlist-url http://服务器IP:8081/allinone.m3u \
  --proxy-url http://服务器IP:19090 \
  --duration 30
```

脚本会从当前直播列表选择房间，并输出 FLV 首帧、接收字节数、平均码率和最长数据间隔。仓库 Actions 页还提供 `Live Playback Smoke Test`：除流量测试外，会使用 FFmpeg 真实解码 60 秒并检查冻结帧和循环画面。

正确结果应满足：

- `/healthz` 输出 `ok`；
- M3U 第一行是 `#EXTM3U`；
- Docker 状态最终变成 `healthy`；
- 从电视或电脑访问 `http://服务器IP:8081/allinone.m3u` 能下载列表。

如果 M3U 暂时只有头部，先等待首次同步并查看：

```bash
docker exec livetv tail -n 100 /data/lnmp/logs/sync-channels.log
docker exec livetv tail -n 100 /data/lnmp/logs/iptv-api-update.log
docker exec livetv tail -n 100 /data/lnmp/logs/bridge.log
```

## 端口到底怎么开

| 宿主端口 | 是否必需 | 谁会访问 | 用途 |
|---:|---|---|---|
| `8081` | 是 | 播放器 | 下载最终 `allinone.m3u` |
| `19090` | 使用虎牙/斗鱼时是 | 播放器 | Go 直播流代理、断流续接和时间戳修正 |
| `19091` | 否 | 旧客户端 | 旧版虎牙 Python 直通兼容 |
| `8080` | 否 | 管理员 | IPTV 页面，建议只在内网使用 |
| `35455` | 否 | 兼容客户端 | Go 单平台 M3U/解析接口 |
| `35456` | 否 | 旧客户端 | Python 旧兼容接口 |

例如 NAS 的 `8081` 已被占用，可以改成 `8201:8081`，播放入口随之改成 `http://NAS地址:8201/allinone.m3u`。流代理端口也能改，但必须同步设置公开端口；仓库自带 Compose 会自动处理，详见 [端口修改实例](docs/docker.md#端口冲突时怎么改)。

## 项目如何筛选直播源

| 层级 | 检查内容 | 能解决的问题 |
|---|---|---|
| 直播目录 | 虎牙/斗鱼官方当前开播房间 | 排除已不在直播目录的房间 |
| 地址解析 | 重新获取有时效性的真实 CDN 地址 | 避免保存已经过期的签名 |
| 实际拉流 | HTTP 状态、FLV 文件头、拉取字节量 | 排除网页、空响应、占位响应 |
| IPTV 检测 | 可播性、速度、清晰度、HLS 状态 | 排除大量失效和低质量源 |
| 发布过滤 | 正时长 `EXTINF`、视频文件扩展名、URL 黑名单 | 排除明显 VOD/录播文件 |
| last-good | 新列表数量过少时拒绝覆盖 | 避免一次网络故障清空好列表 |

发现自动检测漏掉的循环录播后，编辑宿主机文件：

```bash
nano data/lnmp/applecms/iptv-blocklist.txt
```

每行写一个稳定的域名或路径关键字，例如：

```text
# 注释行
recorded.example.invalid
/archive/
```

立即重新过滤：

```bash
docker exec livetv /opt/livetv/scripts/bridge_iptv.sh
```

## 数据、升级和删除

所有需要保留的内容都在宿主机 `./data`：

```text
data/
├── iptv-api/config/                 # 订阅、测速、EPG 等配置
├── iptv-api/output/result.m3u       # IPTV 原始结果
└── lnmp/
    ├── allinone/channels.json       # 当前直播房间目录
    ├── applecms/.iptv-result.m3u    # 过滤后的 IPTV 快照
    ├── applecms/iptv-blocklist.txt  # 自定义过滤词
    └── logs/                        # 日志
```

升级不会删除数据：

```bash
docker compose pull
docker compose up -d --remove-orphans
```

只删除容器但保留配置：

```bash
docker compose down
```

不要随便删除 `data/`。备份、固定版本和回滚方法见 [部署白皮书](docs/docker.md#升级备份与回滚)。

## 文档目录

- [文档中心：按设备、问题和阅读顺序导航](docs/README.md)
- [Docker、Debian、群晖、OpenWrt 部署白皮书](docs/docker.md)
- [系统架构和一次播放请求的完整流程](docs/architecture.md)
- [环境变量、端口、IPTV 配置参考](docs/configuration.md)
- [Android Termux 安装、后台和自启动](docs/termux.md)
- [按现象排查：启动、端口、空列表、播放失败、Actions](docs/troubleshooting.md)
- [Docker Hub 镜像说明](dockerhub-description.md)
- [免责声明](DISCLAIMER.md)

## 开发和构建状态

```bash
go vet ./...
go test ./...
python3 -m py_compile docker/*.py
python3 -m unittest discover -s tests -v
shellcheck -x docker/*.sh docker/scripts/*.sh switch.sh
docker compose -f docker/docker-compose.yml config --quiet
```

每次推送 `main` 会先运行测试，再分别验证 amd64/arm64，最后发布 Docker Hub 和可选的阿里云 ACR。构建状态见 [GitHub Actions](https://github.com/minshurui/livetv-server/actions/workflows/build-image.yml)。

项目没有内置登录系统。不要把 IPTV 管理页和流代理直接暴露到不可信公网；建议使用局域网、VPN，或在带认证的反向代理后使用。不要提交 `.env`、真实 IP、私人路径、SSH 信息和访问令牌。
