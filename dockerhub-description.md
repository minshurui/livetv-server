# livetv-allinone

将虎牙、斗鱼当前开播房间和经过检测的 IPTV 电视源聚合为一个 M3U。镜像支持 `linux/amd64`、`linux/arm64`，包含 Go 续流服务、Python 兼容代理、`iptv-api`、FFmpeg 和 nginx，宿主机不需要安装 Go、Python 或 FFmpeg。

- 源码：<https://github.com/minshurui/livetv-server>
- 完整部署白皮书：<https://github.com/minshurui/livetv-server/blob/main/docs/docker.md>
- 配置参考：<https://github.com/minshurui/livetv-server/blob/main/docs/configuration.md>
- 故障排查：<https://github.com/minshurui/livetv-server/blob/main/docs/troubleshooting.md>

## 最终播放地址

```text
http://服务器IP或域名:8081/allinone.m3u
```

注意：M3U 里的斗鱼和虎牙频道还会连接 `19090`。只映射 8081 会导致“列表能下载但平台频道无法播放”。`19091` 仅供旧版虎牙链接兼容。

## 直接部署

创建 `compose.yml`：

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
      - "19091:19091" # 可选：旧版虎牙兼容端口
      - "8080:8080"   # 可选：IPTV 管理页
    environment:
      TZ: Asia/Shanghai
      PUBLIC_HOST: ""
      HUYA_CDN: AL
      HUYA_CODEC: "264"
      HUYA_MAX_RATIO: "2000"
      HUYA_GROUP_MODE: compact
      DOUYU_GROUP_MODE: compact
      IPTV_REJECT_VOD: "1"
      IPTV_MIN_CHANNELS: "20"
    volumes:
      - ./data:/data
    security_opt:
      - no-new-privileges:true
    stop_grace_period: 20s
    logging:
      options:
        max-size: "10m"
        max-file: "3"
```

启动并检查：

```bash
docker compose pull
docker compose up -d
docker compose ps
docker logs --tail=100 -f livetv
```

本机验证：

```bash
curl -fsS http://127.0.0.1:8081/healthz && echo
curl -fsS http://127.0.0.1:8081/allinone.m3u | sed -n '1,8p'
docker inspect --format '{{.State.Health.Status}}' livetv
```

正确结果是 `/healthz` 输出 `ok`、M3U 第一行是 `#EXTM3U`、容器最终为 `healthy`。

镜像默认完整补抓虎牙“一起看”分类，并把虎牙和斗鱼整理为 8 个大组；频道图标使用主播头像。加载 M3U 时还会后台预解析每组前几个虎牙/斗鱼频道，降低冷启动换台延迟。

## 群晖

在 Container Manager 中新建“项目”，项目目录可选：

```text
/volume1/docker/livetv
```

把上方 Compose 粘贴到项目中。`./data:/data` 会把持久化数据保存到 `/volume1/docker/livetv/data`。启动后使用：

```text
http://群晖IP:8081/allinone.m3u
```

群晖防火墙至少允许播放器所在局域网访问 TCP 8081、19090。使用旧版虎牙链接时才需要 19091；8080 仅建议管理设备在内网访问。

## 端口冲突

如果 8081 被占用：

```yaml
ports:
  - "8201:8081"
environment:
  PUBLIC_LIVETV_PORT: "8201"
```

播放地址改为 `http://服务器:8201/allinone.m3u`。

若流端口也修改，必须同时告诉程序写入新公开端口：

```yaml
ports:
  - "29090:19090"
environment:
  PUBLIC_PROXY_PORT: "29090"
```

## Cloudflare 隧道公网访问

Cloudflare Tunnel 对外只通 80/443，无法直连多个流端口。镜像提供 `STREAM_ENTRY` 统一入口：设置后，M3U 里的虎牙/斗鱼频道地址统一写成 `https://域名/stream/{平台}/{房间号}`，全部走 nginx(8081) 入口，由它在容器内部转发到对应流代理，隧道只需放行一个 HTTP 端口。

Compose 只把 8081 绑到本机供 cloudflared 访问：

```yaml
services:
  livetv:
    image: minshurui/livetv-allinone:latest
    container_name: livetv
    restart: unless-stopped
    init: true
    ports:
      - "127.0.0.1:8081:8081"   # 只供本机 cloudflared 访问
    environment:
      TZ: Asia/Shanghai
      STREAM_ENTRY: "https://tv.example.com"
      IPTV_REJECT_VOD: "1"
      IPTV_MIN_CHANNELS: "20"
    volumes:
      - ./data:/data
```

cloudflared 配置（`~/.cloudflared/config.yml`）：

```yaml
tunnel: <隧道ID>
credentials-file: /root/.cloudflared/<隧道ID>.json
ingress:
  - hostname: tv.example.com
    service: http://localhost:8081
  - service: http_status:404
```

最终播放地址：

```text
https://tv.example.com/allinone.m3u
```

无需在防火墙/路由器上开放其他端口；`STREAM_ENTRY` 需带 `https://` 前缀，且不要以 `/` 结尾。

## 首次启动为什么需要等待

启动顺序：

1. Go、Python、nginx 和 `iptv-api` 启动；
2. 同步虎牙/斗鱼当前直播目录；
3. 斗鱼进行解析和实际拉流健康检查；
4. IPTV 下载候选源并测速；
5. 过滤明显 VOD/黑名单，频道数达到下限后发布。

虎牙/斗鱼通常先出现；IPTV 首次生成可能需要几分钟到几十分钟。不要不断重启容器打断测速。

查看进度：

```bash
docker exec livetv tail -f /data/lnmp/logs/sync-channels.log
docker exec livetv tail -f /data/lnmp/logs/iptv-api-update.log
docker exec livetv tail -f /data/lnmp/logs/bridge.log
```

## 持久化数据

挂载 `./data:/data` 后，升级和重建容器不会丢失：

```text
data/
├── iptv-api/config/                 # 订阅、测速和 EPG 配置
├── iptv-api/output/result.m3u       # IPTV 原始结果
└── lnmp/
    ├── allinone/channels.json       # 当前开播房间
    ├── applecms/.iptv-result.m3u    # 过滤后的 IPTV 快照
    ├── applecms/iptv-blocklist.txt  # 用户黑名单
    └── logs/                        # 日志
```

## 录播和坏源过滤

镜像会过滤正时长 `EXTINF`、MP4/MKV 等明显 VOD 文件、部分结束型 HLS 和用户黑名单。发现已经人工确认的循环录播后：

```bash
nano data/lnmp/applecms/iptv-blocklist.txt
docker exec livetv /opt/livetv/scripts/bridge_iptv.sh
```

黑名单每行一个 URL 域名或路径关键字。持续产生新分片的循环录像在协议层可能与真直播相同，无法保证只靠网络检测百分之百识别。

## 升级和回滚

升级：

```bash
docker compose pull
docker compose up -d --remove-orphans
```

稳定环境可使用完整 Git 提交 SHA 标签代替 `latest`：

```yaml
image: minshurui/livetv-allinone:完整Git提交SHA
```

改回旧 SHA 后执行 `docker compose pull && docker compose up -d --force-recreate` 即可回滚。操作前建议备份 `data/`。

## 安全说明

项目没有内置登录认证。不要把 8080 管理页暴露到不可信公网；公网使用建议放在 VPN 或带认证的反向代理后。不要在 Compose、Issue 或日志中公开订阅、密码、Token、SSH 私钥、个人 IP 和私人路径。
