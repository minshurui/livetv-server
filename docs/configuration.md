# 配置参考与实用模板

大多数用户不需要修改配置：默认端口、动态 Host、录播过滤和 last-good 保护已经启用。本页先给常见场景的完整示例，再解释每个变量。

## 配置放在哪里

使用仓库 Compose：

```bash
cp docker/.env.example docker/.env
nano docker/.env
docker compose -f docker/docker-compose.yml up -d --force-recreate
```

使用单文件 `compose.yml`：把配置写进服务的 `environment:`，宿主机端口写进 `ports:`。

配置优先级从高到低：

1. `docker compose` 执行时的 Shell 环境变量；
2. `docker/.env`；
3. `docker-compose.yml` 中的默认值；
4. 镜像内默认值。

`.env` 已被 Git 忽略。真实域名、IP、代理、NAS 地址和密钥只应保存在部署机器或 GitHub Secrets 中。

## 模板 1：默认局域网部署

```dotenv
LIVETV_IMAGE=minshurui/livetv-allinone:latest
LIVETV_DATA_DIR=../data
PUBLIC_HOST=
TZ=Asia/Shanghai

HOST_LIVETV_PORT=8081
HOST_IPTV_UI_PORT=8080
HOST_AIO_PORT=35455
HOST_PROXY_PORT=19090
HOST_RES_PORT=35456
HOST_PY_PORT=19091

IPTV_MIN_CHANNELS=20
IPTV_REJECT_VOD=1
IPTV_BLOCKLIST=
```

保持 `PUBLIC_HOST` 为空。用哪个 IP 或域名下载 M3U，生成的频道 URL 就跟随哪个 Host。

## 模板 2：8080/8081 已被占用

```dotenv
HOST_LIVETV_PORT=8201
HOST_IPTV_UI_PORT=8200
```

播放地址变为：

```text
http://服务器地址:8201/allinone.m3u
```

只改 M3U 入口不需要改 `PUBLIC_HOST`。

## 模板 3：所有公开端口都换掉

```dotenv
HOST_LIVETV_PORT=8201
HOST_IPTV_UI_PORT=8200
HOST_AIO_PORT=25455
HOST_PROXY_PORT=29090
HOST_RES_PORT=25456
HOST_PY_PORT=29091
```

仓库 Compose 会自动把 `HOST_AIO_PORT`、`HOST_PROXY_PORT`、`HOST_PY_PORT` 写入对应 `PUBLIC_*`，最终 M3U 不会仍指向旧端口。

使用自己写的 Compose 时必须显式配置：

```yaml
ports:
  - "8201:8081"
  - "29090:19090"
  - "29091:19091"
environment:
  PUBLIC_PROXY_PORT: "29090"
  PUBLIC_PY_PORT: "29091"
```

## 模板 4：固定域名或反向代理

```dotenv
PUBLIC_HOST=tv.example.com
HOST_LIVETV_PORT=8081
HOST_PROXY_PORT=19090
HOST_PY_PORT=19091
```

`PUBLIC_HOST` 只填写主机名或 IP：

```text
正确：tv.example.com
正确：192.168.1.50
错误：http://tv.example.com
错误：tv.example.com:8081/path
```

反向代理能正确保留客户端 `Host` 时，仍建议留空。

## 模板 5：加强 last-good 保护

```dotenv
IPTV_MIN_CHANNELS=100
SYNC_MIN_HUYA=50
SYNC_MIN_DOUYU=50
```

这三个值不是“最多保留多少频道”，而是“新结果低于多少时拒绝覆盖旧结果”。首次部署、源数量较少或平台冷门时段，不要设得过高。

## 镜像与数据目录

| 变量 | 默认值 | 说明 |
|---|---|---|
| `LIVETV_IMAGE` | `minshurui/livetv-allinone:latest` | 镜像名；可换成完整提交 SHA 标签 |
| `LIVETV_DATA_DIR` | `../data` | 宿主机持久化目录，仅仓库 Compose 使用 |
| `TZ` | `Asia/Shanghai` | 日志和计划任务时区 |

容器内数据根固定为 `/data`。不要把 `LIVETV_DATA_DIR` 误写成容器内路径。

## 宿主机端口

| 变量 | 默认值 | 需要播放器访问 | 用途 |
|---|---:|---|---|
| `HOST_LIVETV_PORT` | `8081` | 是 | 最终 M3U 和 `/healthz` |
| `HOST_PROXY_PORT` | `19090` | 使用斗鱼时是 | 斗鱼 FLV/HLS 代理 |
| `HOST_PY_PORT` | `19091` | 使用虎牙时是 | 虎牙流代理 |
| `HOST_IPTV_UI_PORT` | `8080` | 否 | `iptv-api` 管理页 |
| `HOST_AIO_PORT` | `35455` | 通常否 | Go 单平台 M3U/解析兼容入口 |
| `HOST_RES_PORT` | `35456` | 通常否 | Python 旧兼容入口 |

`HOST_*` 是 Docker 宿主机左侧端口。播放器实际访问这些端口。

## 容器内部端口和公开端口

一般不要修改容器内部端口：

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `LIVETV_HTTP_PORT` | `8081` | 聚合 nginx |
| `NGINX_HTTP_PORT` | `8080` | `iptv-api` nginx |
| `APP_PORT` | `5180` | `iptv-api` Flask/Gunicorn，仅容器内部使用 |
| `NGINX_RTMP_PORT` | `1935` | `iptv-api` 内部 RTMP |
| `AIO_PORT` | `35455` | Go M3U/解析服务 |
| `PROXY_PORT` | `19090` | Go 斗鱼代理 |
| `RES_PORT` | `35456` | Python 旧解析服务 |
| `PY_PORT` | `19091` | Python 虎牙代理 |

下面三个值决定 M3U 里真正写入的端口：

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `PUBLIC_AIO_PORT` | 跟随 `AIO_PORT` | 单平台解析地址 |
| `PUBLIC_PROXY_PORT` | 跟随 `PROXY_PORT` | 斗鱼频道地址 |
| `PUBLIC_PY_PORT` | 跟随 `PY_PORT` | 虎牙频道地址 |

“列表能下载但全部平台频道连不上”，首先检查公开端口是否与宿主映射一致。

## Host、域名和 IPv6

| 变量 | 默认值 | 建议 |
|---|---|---|
| `PUBLIC_HOST` | 空 | 通常留空，自动跟随请求 Host |

IPv4：

```text
http://192.168.1.50:8081/allinone.m3u
```

IPv6：

```text
http://[2001:db8::50]:8081/allinone.m3u
```

程序会规范化 IPv6 方括号。不要把示例地址写入项目默认配置。

## IPTV 发布保护和录播过滤

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `IPTV_MIN_CHANNELS` | `20` | 过滤后少于该数量时保留旧快照 |
| `IPTV_REJECT_VOD` | `1` | `1` 排除明显 VOD，`0` 允许 |
| `IPTV_BLOCKLIST` | 空 | 逗号分隔 URL 关键字，适合临时规则 |
| `IPTV_BLOCKLIST_FILE` | `/data/lnmp/applecms/iptv-blocklist.txt` | 持久化黑名单文件 |

长期规则建议写文件：

```bash
nano data/lnmp/applecms/iptv-blocklist.txt
```

```text
# 每行一个，不区分大小写
recorded.example.invalid
/archive/
```

应用：

```bash
docker exec livetv /opt/livetv/scripts/bridge_iptv.sh
```

过滤器会识别常见文件扩展名、明显 VOD 查询参数、正时长 `EXTINF` 和黑名单，但不能保证识别所有内容型循环直播。

## 虎牙/斗鱼目录同步

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `SYNC_HUYA_PAGES` | `15` | 虎牙最多抓取页数 |
| `SYNC_DOUYU_PAGES` | `20` | 斗鱼最多抓取页数 |
| `SYNC_MIN_HUYA` | `20` | 虎牙新目录最低房间数 |
| `SYNC_MIN_DOUYU` | `20` | 斗鱼新目录最低房间数 |

容器启动时同步一次，之后每 6 小时同步。最低数量只保护目录文件不被异常响应覆盖，不能代替真实拉流检测。

## 代理和 HLS 生命周期

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `UPSTREAM_PROXY` | 空 | Python 上游请求使用的 HTTP/SOCKS 代理 URL |
| `IPV6_PLATFORMS` | 空 | 逗号分隔，需要强制 IPv6 的平台名 |
| `HLS_IDLE` | `300` | HLS 无访问多少秒后回收 FFmpeg |
| `HLS_STALE` | `25` | 播放列表多久未更新视为卡死 |
| `HLS_WARMUP` | `45` | 首次生成 HLS 的最长宽限时间 |

示例：

```dotenv
UPSTREAM_PROXY=http://192.168.1.2:7890
IPV6_PLATFORMS=huya,douyu
```

不要盲目强制 IPv6。宿主机能获得 IPv6 地址，不等于 Docker 网络和平台 CDN 的 IPv6 路径可用。

## `iptv-api` 配置目录

首次启动会复制默认配置到：

```text
<LIVETV_DATA_DIR>/iptv-api/config/
```

常见文件：

| 文件 | 作用 |
|---|---|
| `config.ini` | 更新时间、并发、测速、最低分辨率、EPG 等 |
| `subscribe.txt` | IPTV 候选订阅地址 |
| `blacklist.txt` | 抓取和测速前的 URL 黑名单 |
| `whitelist.txt` | 上游白名单 |

修改后：

```bash
docker compose restart livetv
docker exec livetv tail -f /data/lnmp/logs/iptv-api-update.log
```

两层黑名单不要混淆：

| 文件 | 生效阶段 | 适合用途 |
|---|---|---|
| `iptv-api/config/blacklist.txt` | 收集和测速前 | 减少无效请求 |
| `lnmp/applecms/iptv-blocklist.txt` | 最终发布前 | 阻止漏网录播或坏源进入聚合列表 |

## 可选：从 NAS 同步现成 IPTV 结果

仅在非 Docker/Termux 精简场景中使用。三个变量缺一不可：

| 变量 | 必填 | 说明 |
|---|---|---|
| `NAS_HOST` | 是 | NAS 域名或 IP |
| `NAS_USER` | 是 | SSH 用户 |
| `NAS_M3U` | 是 | NAS 上 M3U 的绝对路径 |
| `DST_FILE` | 否 | 本地目标文件 |
| `MIN_LINES` | 否 | 最低频道数，默认 20 |

```bash
NAS_HOST=nas.example.internal \
NAS_USER=your-user \
NAS_M3U=/path/to/result.m3u \
bash docker/scripts/sync_iptv_from_nas.sh
```

使用 SSH 密钥和主机指纹验证。不要把密码、私钥或真实地址写进仓库。

## GitHub Actions 发布配置

发布 job 使用 GitHub Environment `minshurui`，也兼容仓库级 Actions Secrets。所需名称：

```text
DOCKERHUB_USERNAME
DOCKERHUB_TOKEN
ALIYUN_REGISTRY
ALIYUN_NAMESPACE
ALIYUN_REPO
ALIYUN_USERNAME
ALIYUN_PASSWORD
```

Docker Hub 两项必需；阿里云五项必须全部配置或全部留空。Environment 中的同名 Secret 优先。不要使用 GitHub PAT 代替 Docker Hub 或阿里云令牌。
