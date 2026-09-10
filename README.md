# livetv-server

[![Build](https://github.com/minshurui/livetv-server/actions/workflows/build-image.yml/badge.svg)](https://github.com/minshurui/livetv-server/actions/workflows/build-image.yml)
[![Docker Image](https://img.shields.io/docker/v/minshurui/livetv-allinone?label=Docker%20Hub)](https://hub.docker.com/r/minshurui/livetv-allinone)

把虎牙、斗鱼当前开播房间和经过检测的 IPTV 电视源整理成一个 M3U 播放列表。项目提供 Docker 一键部署，支持 `linux/amd64` 和 `linux/arm64`。

最终给播放器使用的地址只有一个：

```text
http://服务器地址:8081/allinone.m3u
```

## 它解决什么问题

- 自动获取虎牙、斗鱼官方目录里的当前开播房间。
- 解析会过期的真实播放地址，播放器不需要保存 CDN 签名。
- 对斗鱼进行“解析成功 + 实际拉流”两段健康检查。
- 使用 `iptv-api` 对电视源进行可播放性、速度、分辨率和部分广告占位检测。
- 发布电视源前再次过滤明显的 VOD 文件、正时长 M3U 项和自定义黑名单。
- 新结果异常或频道数太少时保留上一次可用列表，不用坏数据覆盖好数据。
- 配置、输出和日志统一持久化到 `/data`，重启后继续定时更新。

## 三分钟部署

需要 Docker 24+ 和 Docker Compose v2。

```bash
git clone https://github.com/minshurui/livetv-server.git
cd livetv-server
cp docker/.env.example docker/.env
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml up -d --no-build
docker compose -f docker/docker-compose.yml ps
```

确认服务：

```bash
curl -fsS http://127.0.0.1:8081/healthz
curl -fsS http://127.0.0.1:8081/allinone.m3u | head
```

看到 `ok` 和 `#EXTM3U` 后，将下面地址加入支持 M3U 的播放器：

```text
http://你的服务器IP或域名:8081/allinone.m3u
```

首次启动时，虎牙/斗鱼列表通常先出现；电视源需要完成下载和测速，可能需要几分钟到几十分钟。可以继续使用服务，不必反复重启容器。

## 常用入口

| 地址 | 用途 | 是否建议公网开放 |
|---|---|---|
| `:8081/allinone.m3u` | 最终聚合播放列表 | 仅可信网络 |
| `:8081/healthz` | 基础探活 | 可以 |
| `:8080` | `iptv-api` 管理和结果页面 | 不建议 |
| `:19090` | 斗鱼流代理 | 播放器需要 |
| `:19091` | 虎牙流代理 | 播放器需要 |
| `:35455` | 单平台 M3U/解析兼容入口 | 按需 |
| `:35456` | Python 旧兼容解析入口 | 通常不需要 |

本项目没有内置登录认证。不要把管理页面和代理端口直接暴露到不可信公网；建议放在局域网、VPN 或带认证的反向代理后面。

## 怎样区分直播和录播

项目采用多层判断，但不会假装可以 100% 理解视频内容：

| 检查 | 能排除什么 |
|---|---|
| 官方直播目录 | 已下播或不在当前直播列表的房间 |
| 真实地址解析 | 空地址、失效签名和测试回退地址 |
| FLV 魔数与实际拉流 | HTML 错误页、占位响应、只有少量数据的假活源 |
| `iptv-api` 测速/分辨率/HLS 检查 | 无法播放、太慢、低清、短广告/无信号清单 |
| 发布前 VOD 过滤 | 正时长 `EXTINF`、MP4/MKV 等文件型录播 |
| 自定义 URL 黑名单 | 已知录播域名或路径 |

长时间循环且持续生成新 HLS 分片的录播，在协议层和真直播完全相同，无法只靠网络探测绝对识别。发现这种源后，把其域名或稳定路径写入：

```text
data/lnmp/applecms/iptv-blocklist.txt
```

每行一个关键字，例如：

```text
example-recorded-domain.invalid
/archive/
```

下一次桥接结果时会自动过滤，不需要修改镜像。

## 数据在哪里

默认宿主机目录为仓库根目录的 `data/`：

```text
data/
├── iptv-api/config/                 # iptv-api 可编辑配置
├── iptv-api/output/result.m3u       # iptv-api 原始结果
└── lnmp/
    ├── allinone/channels.json       # 虎牙/斗鱼当前直播房间
    ├── applecms/.iptv-result.m3u    # 过滤后、最终参与聚合的电视源
    ├── applecms/iptv-blocklist.txt  # 自定义录播/坏源黑名单
    └── logs/                        # 各组件日志
```

升级或重建容器不会删除这个目录。

## 文档导航

- [系统架构与源检测流程](docs/architecture.md)
- [Docker、群晖部署、升级和多架构构建](docs/docker.md)
- [完整环境变量和配置文件说明](docs/configuration.md)
- [Termux 安装和后台运行](docs/termux.md)
- [按现象排查故障](docs/troubleshooting.md)
- [免责声明](DISCLAIMER.md)

## 开发验证

```bash
go test ./...
python3 -m py_compile docker/*.py
python3 -m unittest discover -s tests -v
sh -n docker/entry.sh docker/healthcheck.sh docker/scripts/bridge_iptv.sh
bash -n switch.sh docker/scripts/sync_iptv_from_nas.sh
docker buildx build --platform linux/amd64,linux/arm64 --output type=cacheonly .
```

构建状态：[GitHub Actions](https://github.com/minshurui/livetv-server/actions/workflows/build-image.yml)。镜像地址：[Docker Hub](https://hub.docker.com/r/minshurui/livetv-allinone)。

请勿提交 `.env`、真实 IP、个人目录、频道数据、SSH 信息、访问令牌或其他密钥。曾经公开发送或提交过的令牌应立即撤销并重新生成。
