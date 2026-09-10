# 故障排查

先执行这组命令，它能区分“容器没启动”“核心进程退出”和“列表有了但上游源失败”：

```bash
docker compose -f docker/docker-compose.yml ps
docker inspect --format '{{json .State.Health}}' livetv
docker logs --tail=200 livetv
curl -v --max-time 10 http://127.0.0.1:8081/healthz
curl -v --max-time 10 http://127.0.0.1:8081/allinone.m3u
```

## 症状速查

| 症状 | 最可能原因 | 先做什么 |
|---|---|---|
| Compose 启动时报端口占用 | 8080/8081/19090/19091 已被使用 | 修改 `docker/.env` 的 `HOST_*_PORT` |
| 容器一直 `starting` | 首次启动未完成或某个核心进程反复退出 | 查看 `docker inspect` 和下方日志 |
| `/healthz` 正常但 Docker 为 unhealthy | Go、虎牙代理或 `iptv-api` 进程失败 | `docker logs` 后检查组件日志 |
| M3U 只有 `#EXTM3U` | `channels.json` 尚未生成，电视源也未桥接 | 看 `sync-channels.log` 和 `iptv-api-update.log` |
| M3U 有频道但播放超时 | 播放器无法访问 19090/19091 或上游被网络拦截 | 测试流端口和具体频道 URL |
| M3U 中 IP 正确但端口错误 | 宿主端口改过，`PUBLIC_*_PORT` 未同步 | 使用仓库 Compose 或手动设置公开端口 |
| IPTV 更新失败但旧频道还能播 | last-good 保护生效 | 查 `bridge.log` 的失败原因 |
| 出现录播/循环频道 | 协议上仍像直播，未命中结构过滤 | 将域名/路径加入 `iptv-blocklist.txt` |
| buildx 卡很久 | QEMU、基础镜像拉取或旧 Dockerfile 源码编译 | 使用最新 main，并查看具体架构 job |

## 日志位置

```bash
docker exec livetv ls -lh /data/lnmp/logs
docker exec livetv tail -n 200 /data/lnmp/logs/livetv.log
docker exec livetv tail -n 200 /data/lnmp/logs/huya-proxy.log
docker exec livetv tail -n 200 /data/lnmp/logs/sync-channels.log
docker exec livetv tail -n 200 /data/lnmp/logs/iptv-api.log
docker exec livetv tail -n 200 /data/lnmp/logs/iptv-api-update.log
docker exec livetv tail -n 200 /data/lnmp/logs/bridge.log
```

## 容器无法启动

检查配置展开后的最终 Compose：

```bash
docker compose -f docker/docker-compose.yml config
```

检查端口：

```bash
docker ps --format 'table {{.Names}}\t{{.Ports}}'
ss -lntp | grep -E ':8080|:8081|:19090|:19091|:35455|:35456'
```

修改 `docker/.env`，例如：

```dotenv
HOST_LIVETV_PORT=8201
HOST_IPTV_UI_PORT=8200
```

然后重建容器：

```bash
docker compose -f docker/docker-compose.yml up -d --force-recreate
```

## 容器 unhealthy

查看健康检查最近输出：

```bash
docker inspect --format '{{range .State.Health.Log}}{{.End}} exit={{.ExitCode}} {{.Output}}{{println}}{{end}}' livetv
```

手动执行同一个检查：

```bash
docker exec livetv /opt/livetv/healthcheck.sh
docker exec livetv sh -c 'for f in /run/livetv-*.pid; do echo "$f $(cat "$f")"; done'
```

入口脚本会每 20 秒重启退出的子进程。如果同一进程不断重启，真正原因通常在该进程对应日志中，而不是健康检查本身。

## 频道列表为空

确认房间目录文件：

```bash
docker exec livetv sh -c 'ls -lh /data/lnmp/allinone/channels.json; head -c 200 /data/lnmp/allinone/channels.json'
```

手动同步一次：

```bash
docker exec livetv python3 /opt/livetv/sync_channels.py \
  --out /data/lnmp/allinone/channels.json
```

如果平台只返回少量房间，脚本会保留旧文件并记录数量。首次启动没有旧文件时，可临时降低保护下限：

```dotenv
SYNC_MIN_HUYA=5
SYNC_MIN_DOUYU=5
```

这只影响目录是否落盘，不会绕过后续真实地址和拉流检查。

## 电视源一直没有加入聚合列表

按顺序检查：

```bash
docker exec livetv ls -lh /data/iptv-api/output/result.m3u
docker exec livetv /opt/livetv/scripts/bridge_iptv.sh
docker exec livetv tail -n 50 /data/lnmp/logs/bridge.log
docker exec livetv ls -lh /data/lnmp/applecms/.iptv-result.m3u
```

常见桥接提示：

- `输入不存在`：`iptv-api` 尚未完成首次生成，继续看更新日志。
- `仅剩 N 个频道，低于下限`：候选源失效较多，旧结果被正确保留；检查订阅或降低 `IPTV_MIN_CHANNELS`。
- `blocklist=N`：命中自定义 URL 黑名单。
- `positive_duration=N` / `vod_file=N`：检测到明显录播/VOD 条目。

## 地址或端口不正确

正常情况下，访问：

```text
http://192.0.2.10:8081/allinone.m3u
```

列表会使用请求 Host `192.0.2.10`。若反向代理重写 Host，可设置：

```dotenv
PUBLIC_HOST=tv.example.com
```

这里不要填写协议或路径。IPv6 可填写裸地址，程序会自动生成 `[IPv6地址]`。

如果宿主机把 `19090` 映射成 `29090`，必须让列表写 `29090`：

```dotenv
HOST_PROXY_PORT=29090
```

仓库 Compose 会自动设置 `PUBLIC_PROXY_PORT=29090`。自定义 Compose 需要自己同时设置两者。

## 单个频道播放失败

从 M3U 复制该频道下一行 URL，然后测试：

```bash
curl -v --max-time 15 -o /dev/null '频道URL'
```

含义：

- `404 offline (room not live)`：房间已下播或解析不到有效直播；这是正常离线状态，不再跳转测试录像。
- `502 stream not FLV`：上游返回网页、错误体或其他非 FLV 内容。
- 连接服务器端口超时：Docker 端口、防火墙或路由问题。
- 已连接但很快中断：查看 `livetv.log` / `huya-proxy.log`，可能是 CDN 限流或签名变化。

## GitHub Actions / buildx 失败

构建页面：<https://github.com/minshurui/livetv-server/actions/workflows/build-image.yml>。

工作流顺序是：

1. Go、Python、Shell 测试。
2. amd64 和 arm64 分开验证镜像。
3. 两个架构都通过后，主线才推送 Docker Hub/阿里云。

`secrets` 不能直接用于某些 job/step 条件表达式。当前工作流先把 Secrets 映射到 job 的 `env`，条件只读取 `env.*`。如果又看到“无法识别的命名值 secrets”，说明运行的不是最新工作流提交。

若提示 Docker Hub 未授权，检查仓库 Actions Secrets：

- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`（Docker Hub Access Token，不是 GitHub Token）

公开发送过的 GitHub 或 Docker Hub 令牌必须撤销，不能继续使用。
