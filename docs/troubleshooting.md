# 故障排查手册

排障时先判断问题发生在哪一层：容器、M3U 入口、频道目录、直播代理、IPTV 更新，还是 GitHub Actions。不要一看到频道为空就反复重建容器。

## 一分钟总检查

在 `compose.yml` 所在目录执行：

```bash
docker compose ps
docker inspect --format '{{json .State.Health}}' livetv
docker logs --tail=200 livetv
curl -v --max-time 10 http://127.0.0.1:8081/healthz
curl -v --max-time 10 http://127.0.0.1:8081/allinone.m3u
```

判断方法：

| 结果 | 说明 | 下一步 |
|---|---|---|
| 容器不存在 | Compose 没启动或目录不对 | `docker compose up -d` |
| 容器反复重启 | 主进程初始化失败 | 看 `docker logs livetv` |
| `healthz` 连接拒绝 | 8081 没监听或映射错误 | 检查端口和健康状态 |
| `healthz=ok`，M3U 只有头 | 服务正常，频道数据尚未生成 | 检查同步/桥接日志 |
| M3U 有频道，本机可播，电视不可播 | 网络、防火墙或公开端口错误 | 检查 8081/19090；19091 仅用于旧虎牙链接 |
| 只有某个平台失败 | 该平台解析或上游网络问题 | 看平台对应日志 |

## 症状索引

| 症状 | 直接跳转 |
|---|---|
| 端口被占用 | [容器启动失败](#容器启动失败或端口被占用) |
| 容器一直 `starting` / `unhealthy` | [健康检查](#容器一直-starting-或-unhealthy) |
| M3U 下载不到 | [入口错误](#m3u-入口访问失败) |
| M3U 只有 `#EXTM3U` | [空列表](#m3u-只有-extm3u) |
| IPTV 一直不出现 | [IPTV 更新](#iptv-电视源一直没有加入) |
| 列表有了但频道打不开 | [播放失败](#列表能下载但频道播放失败) |
| IP/端口写错 | [地址生成](#m3u-里的-ip域名或端口错误) |
| 出现录播/循环源 | [过滤漏网](#仍然出现录播或循环频道) |
| Actions 红叉 | [构建发布](#github-actions-构建或发布失败) |

## 日志地图

| 日志 | 对应问题 |
|---|---|
| `docker logs livetv` | 容器初始化、进程退出、重启 |
| `/data/lnmp/logs/livetv.log` | Go 解析、斗鱼健康检查、流代理 |
| `/data/lnmp/logs/huya-proxy.log` | 虎牙拉流和断流 |
| `/data/lnmp/logs/sync-channels.log` | 虎牙/斗鱼直播目录同步 |
| `/data/lnmp/logs/iptv-api.log` | `iptv-api` Web/内部服务 |
| `/data/lnmp/logs/iptv-api-update.log` | IPTV 候选下载、测速和输出 |
| `/data/lnmp/logs/bridge.log` | IPTV 发布过滤、数量保护 |

一次查看：

```bash
docker exec livetv sh -c '
for f in /data/lnmp/logs/*.log; do
  echo "===== $f ====="
  tail -n 40 "$f"
done
'
```

该命令不会显示 `.env` 或 GitHub Secrets，但平台返回内容仍可能包含订阅 URL；对外发送前请检查。

## 容器启动失败或端口被占用

### 先展开最终 Compose

```bash
docker compose config
docker compose ps
```

### 检查被占用端口

```bash
docker ps --format 'table {{.Names}}\t{{.Ports}}'
ss -lntp | grep -E ':8080|:8081|:19090|:19091|:35455|:35456' || true
```

若看到 `address already in use`，修改宿主机左侧端口。仓库 `.env` 示例：

```dotenv
HOST_LIVETV_PORT=8201
HOST_IPTV_UI_PORT=8200
HOST_PROXY_PORT=29090
HOST_PY_PORT=29091
```

应用：

```bash
docker compose -f docker/docker-compose.yml up -d --force-recreate
```

此时播放地址是 `http://服务器:8201/allinone.m3u`。仓库 Compose 会同步流代理公开端口；自定义 Compose 必须设置 `PUBLIC_PROXY_PORT`。`PUBLIC_PY_PORT` 只影响旧版虎牙链接。

## 容器一直 `starting` 或 `unhealthy`

健康状态不是只检查 nginx，还会确认核心进程、M3U 和 IPTV 页面。

```bash
docker inspect --format '{{range .State.Health.Log}}{{.End}} exit={{.ExitCode}} {{.Output}}{{println}}{{end}}' livetv
docker exec livetv /opt/livetv/healthcheck.sh
docker exec livetv sh -c 'for f in /run/livetv-*.pid; do echo "$f $(cat "$f")"; done'
```

常见原因：

- 首次启动还在初始化；
- 端口冲突导致子进程无法监听；
- 上游基础配置不完整；
- 数据目录不可写；
- 内存不足，进程被 OOM Kill。

检查资源和挂载：

```bash
docker inspect --format '{{.State.OOMKilled}}' livetv
docker exec livetv sh -c 'id; ls -ld /data; touch /data/.write-test && unlink /data/.write-test'
df -h
free -h
```

## M3U 入口访问失败

本机测试：

```bash
curl -v --max-time 10 http://127.0.0.1:8081/healthz
curl -v --max-time 10 http://127.0.0.1:8081/allinone.m3u
```

如果本机失败：检查容器状态和 `HOST_LIVETV_PORT`。如果本机成功、其他设备失败：

1. 使用服务器局域网 IP，不要使用 `127.0.0.1`；
2. 确认客户端与服务器路由可达；
3. 放行 TCP 8081；
4. 群晖检查 DSM 防火墙；
5. 云服务器检查安全组；
6. OpenWrt 检查防火墙区域和端口转发。

如果实际映射成 `8201:8081`，测试地址必须使用 `8201`。

## M3U 只有 `#EXTM3U`

这通常表示程序正常，但三个来源暂时都没有可发布数据。

### 检查直播目录

```bash
docker exec livetv sh -c '
ls -lh /data/lnmp/allinone/channels.json
head -c 300 /data/lnmp/allinone/channels.json
'
docker exec livetv tail -n 100 /data/lnmp/logs/sync-channels.log
```

手动同步：

```bash
docker exec livetv python3 /opt/livetv/sync_channels.py \
  --out /data/lnmp/allinone/channels.json
```

首次启动没有旧文件、平台返回数量又低于保护值时，可暂时降低：

```dotenv
SYNC_MIN_HUYA=5
SYNC_MIN_DOUYU=5
```

这只允许目录写入，不会把解析失败的房间伪装成直播。

### 检查 IPTV 快照

```bash
docker exec livetv ls -lh /data/iptv-api/output/result.m3u
docker exec livetv ls -lh /data/lnmp/applecms/.iptv-result.m3u
docker exec livetv tail -n 100 /data/lnmp/logs/bridge.log
```

## IPTV 电视源一直没有加入

按顺序执行：

```bash
docker exec livetv tail -n 150 /data/lnmp/logs/iptv-api-update.log
docker exec livetv ls -lh /data/iptv-api/output/result.m3u
docker exec livetv /opt/livetv/scripts/bridge_iptv.sh
docker exec livetv tail -n 80 /data/lnmp/logs/bridge.log
```

桥接日志含义：

| 提示 | 含义 | 处理 |
|---|---|---|
| `输入不存在` | `iptv-api` 尚未生成原始结果 | 检查订阅和更新日志 |
| `低于下限` | 新结果太少，last-good 生效 | 检查失效源或调整 `IPTV_MIN_CHANNELS` |
| `blocklist=N` | 命中用户黑名单 | 检查 `iptv-blocklist.txt` |
| `positive_duration=N` | M3U 标记了正时长，按 VOD 排除 | 确认是否应设置 `IPTV_REJECT_VOD=0` |
| `vod_file=N` | URL 像 MP4/MKV 等文件 | 正常过滤；误判时关闭 VOD 过滤 |

若 `subscribe.txt` 中的来源被网络阻断，需要更换来源或为上游配置代理，不能靠反复重启解决。

## 列表能下载但频道播放失败

从 M3U 中复制某个频道 URL：

```bash
curl -v --max-time 15 -o /dev/null '频道URL'
```

返回含义：

| 结果 | 含义 |
|---|---|
| `404 offline (room not live)` | 房间已下播或当前无法解析；不会再跳测试录像 |
| `502 stream not FLV` | 上游返回网页、错误体或非 FLV 内容 |
| 连接 19090 超时 | 播放器到新版虎牙/斗鱼代理端口不通 |
| 连接 19091 超时 | 只影响旧版虎牙直通链接 |
| 建立连接后很快断开 | CDN 限流、签名变化或上游网络问题 |

平台对应日志：

```bash
# 虎牙/斗鱼 Go 续流层
docker exec livetv tail -n 150 /data/lnmp/logs/livetv.log

# 仅旧版 19091 虎牙链接
docker exec livetv tail -n 150 /data/lnmp/logs/huya-proxy.log
```

IPTV 直链不经过 19090/19091。只有 IPTV 失败时，应直接测试该上游 URL 和服务器网络。

### 虎牙 403、播几秒断流或反复卡顿

新版本默认让虎牙走 `19090` Go 续流层，并使用当前签名参数生成 H.264 FLV。先确认 M3U 中虎牙地址形如：

```text
http://服务器:19090/stream/huya/房间号
```

不要只用 `/healthz` 判断播放正常，它不会访问虎牙上游。部署后可在仓库目录执行真实流测试：

```bash
python3 tests/live_stream_smoke.py \
  --playlist-url http://服务器IP:8081/allinone.m3u \
  --proxy-url http://服务器IP:19090 \
  --duration 30 \
  --report huya-smoke-report.json
```

通过标准是响应以 `FLV` 开头、30 秒至少接收 1 MiB，并且 4 KiB 即时读取的任意两次数据到达间隔不超过 0.5 秒。报告同时给出 `gap_p95_seconds`、`gap_p99_seconds` 和 `longest_gap_seconds`，避免把大块缓冲填充时间误判成断粮。Actions 中的 `Live Playback Smoke Test` 还会用 FFmpeg 解码 60 秒，以每秒 2 帧生成画面 MD5；连续相同画面超过 8 秒，或 10 秒以内的片段重复至少 3 次且持续 12 秒，都会判定失败。公网 GitHub Runner 的结果只能验证解析和代理链路；最终仍应在实际群晖和家庭网络再执行一次，因为地区、运营商和播放器缓冲策略都会影响体验。

如果仍是 `19091`，说明播放器缓存了旧 M3U；删除旧订阅后重新添加。继续失败时查看：

```bash
docker exec livetv tail -n 150 /data/lnmp/logs/livetv.log
docker logs --tail=150 livetv
```

默认首选 `HUYA_CDN=AL`。只有日志持续出现该线路 403/连接失败时，才在 Compose 中依次尝试 `HUYA_CDN=TX` 或 `HUYA_CDN=HS`，重建容器并重新加载列表。`HUYA_CODEC=264` 建议保持不变，避免电视端不支持 HEVC FLV。

## M3U 里的 IP、域名或端口错误

访问：

```text
http://192.168.1.50:8081/allinone.m3u
```

默认生成 `192.168.1.50`。若反向代理改写了 Host，可设置：

```dotenv
PUBLIC_HOST=tv.example.com
```

不要带协议、端口和路径。

如果宿主机映射 `29090:19090`，M3U 必须写 `29090`：

```dotenv
HOST_PROXY_PORT=29090
```

仓库 Compose 会自动同步。自定义 Compose：

```yaml
environment:
  PUBLIC_PROXY_PORT: "29090"
```

IPv6 地址应按 `http://[IPv6]:8081/allinone.m3u` 访问，程序会自动保留方括号。

## 仍然出现录播或循环频道

自动过滤能识别文件型 VOD、正时长 M3U 和部分 HLS 结束标志。持续产生新分片的循环视频与真直播协议形态相同，无法保证自动识别。

确认是录播后，把稳定域名或路径写入：

```bash
nano data/lnmp/applecms/iptv-blocklist.txt
docker exec livetv /opt/livetv/scripts/bridge_iptv.sh
docker exec livetv tail -n 50 /data/lnmp/logs/bridge.log
```

不要用过短的关键字，例如 `live`、`cdn`，否则会误伤大量正常频道。

## GitHub Actions 构建或发布失败

构建页：<https://github.com/minshurui/livetv-server/actions/workflows/build-image.yml>。

顺序：

1. Go/Python/Shell/Compose 测试；
2. amd64、arm64 分开验证；
3. 两个架构通过后，Docker Hub 与阿里云 ACR 进入相互独立的发布 job；
4. Docker Hub 镜像推送成功后更新仓库说明；
5. 阿里云未配置时明确跳过，配置不完整时只让阿里云 job 失败。

因此 Docker Hub 凭据错误不会再阻止阿里云发布，反过来也一样。

### `secrets` 无法识别

workflow 的 `if:` 不直接读取 `secrets.*`，而是先映射到 job `env`。看到旧错误说明运行的是旧提交，请确认 `main` 已更新。

### 提示缺少 Docker Hub 参数

本项目发布 job 声明：

```yaml
environment: minshurui
```

因此凭证应放在：

```text
Settings → Environments → minshurui → Environment secrets
```

也兼容仓库级：

```text
Settings → Secrets and variables → Actions
```

Secret 名称必须完全一致：

```text
DOCKERHUB_PASSWORD
DOCKERHUB_TOKEN
```

两项二选一：`DOCKERHUB_PASSWORD` 用于兼容原先成功的账号密码登录；`DOCKERHUB_TOKEN` 是 Docker Hub Access Token，不是 GitHub PAT。两项同时存在时优先使用密码。Docker Hub 用户名默认取 GitHub 仓库 owner；两者不同时，在 Actions Variables 中设置 `DOCKERHUB_USERNAME`，不要把公开用户名放进 Secret。

如果在 `Login to Docker Hub` 直接出现：

```text
unauthorized: incorrect username or password
```

这发生在推送之前，表示凭据本身未通过身份验证，不是镜像仓库写入权限不足。使用旧密码方案时，把可登录 Docker Hub 的账号密码保存为 `DOCKERHUB_PASSWORD`；workflow 会优先使用它。GitHub 无法读取已有 Secret 的明文，因此更新后需重新运行构建验证。

如果登录步骤成功，但推送时报：

```text
401 Unauthorized: access token has insufficient scopes
```

说明 Token 有效但只有读取权限。到 Docker Hub 重新创建或编辑具备 **Read & Write** 权限的 Access Token，再替换 Environment 中的 `DOCKERHUB_TOKEN`。只读 Token 可以登录和拉取，不能推送镜像，也不能完成说明同步。

### 阿里云配置不完整

以下五项必须全部配置或全部留空：

```text
ALIYUN_REGISTRY
ALIYUN_NAMESPACE
ALIYUN_REPO
ALIYUN_USERNAME
ALIYUN_PASSWORD
```

如果阿里云登录成功、推送时出现：

```text
unknown manifest class for application/vnd.oci.empty.v1+json
```

说明当前 ACR 实例不兼容 Buildx 的 provenance/SBOM OCI 附件。项目在阿里云发布步骤中已关闭这两类附件，但仍会发布标准的 amd64/arm64 多架构 manifest；Docker Hub 发布不受影响。

### buildx / apk 下载失败

先确认失败的是 amd64 还是 arm64 job。最新 Dockerfile 使用固定 digest 的多架构 `iptv-api` 基础镜像，并对 APK 安装重试；不要继续运行旧源码构建提交。

网络瞬时失败可以在 Actions 页面选择 **Re-run failed jobs**。如果反复在相同包或基础镜像失败，再检查 Docker Hub/Alpine 网络与固定 digest 是否仍可用。

公开发送过的 GitHub、Docker Hub 或阿里云令牌都应撤销并重新生成。
