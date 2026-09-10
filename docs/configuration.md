# 配置参考

Docker 用户复制示例文件后修改：

```bash
cp docker/.env.example docker/.env
nano docker/.env
docker compose -f docker/docker-compose.yml up -d
```

`.env` 已被 Git 忽略。真实域名、IP、代理、NAS 地址和密钥只应保存在部署机器或平台的 Secrets 中。

## 最常用配置

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `LIVETV_IMAGE` | `minshurui/livetv-allinone:latest` | 部署镜像，可换成提交 SHA 标签回滚 |
| `LIVETV_DATA_DIR` | `../data` | 宿主机持久化目录 |
| `PUBLIC_HOST` | 空 | 为空时跟随请求 Host；反代不能保留 Host 时再填写域名/IP |
| `TZ` | `Asia/Shanghai` | 日志、任务和更新时间时区 |
| `IPTV_MIN_CHANNELS` | `20` | 过滤后少于此数量时拒绝覆盖 last-good 快照 |
| `IPTV_REJECT_VOD` | `1` | `1` 过滤明显 VOD，`0` 允许正时长/文件型条目 |
| `IPTV_BLOCKLIST` | 空 | 逗号分隔的 URL 关键字，适合少量临时规则 |

长期黑名单请编辑：

```text
<LIVETV_DATA_DIR>/lnmp/applecms/iptv-blocklist.txt
```

每行一个关键字，`#` 开头为注释。

## 宿主机端口

仓库 Compose 支持直接修改左侧宿主机端口：

| 变量 | 默认值 | 用途 |
|---|---:|---|
| `HOST_LIVETV_PORT` | `8081` | 最终 M3U 和基础探活 |
| `HOST_IPTV_UI_PORT` | `8080` | `iptv-api` Web 页面 |
| `HOST_AIO_PORT` | `35455` | Go M3U/解析兼容接口 |
| `HOST_PROXY_PORT` | `19090` | 斗鱼代理，聚合列表需要 |
| `HOST_RES_PORT` | `35456` | Python 旧解析接口 |
| `HOST_PY_PORT` | `19091` | 虎牙代理，聚合列表需要 |

例如 8081 被占用：

```dotenv
HOST_LIVETV_PORT=8201
```

入口随之变成 `http://服务器:8201/allinone.m3u`。

## 内部端口与公开端口

通常不要修改内部端口。自定义编排时可使用：

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `LIVETV_HTTP_PORT` | `8081` | 容器内聚合 nginx |
| `NGINX_HTTP_PORT` | `8080` | 容器内 `iptv-api` nginx |
| `APP_PORT` | `5180` | 容器内 Flask，仅供 nginx 使用 |
| `NGINX_RTMP_PORT` | `1935` | 容器内 RTMP |
| `AIO_PORT` | `35455` | Go 列表/解析服务 |
| `PROXY_PORT` | `19090` | Go 流代理 |
| `RES_PORT` | `35456` | Python 兼容解析服务 |
| `PY_PORT` | `19091` | Python 虎牙代理 |
| `PUBLIC_AIO_PORT` | 跟随 `AIO_PORT` | 写入 M3U 的外部解析端口 |
| `PUBLIC_PROXY_PORT` | 跟随 `PROXY_PORT` | 写入 M3U 的外部斗鱼端口 |
| `PUBLIC_PY_PORT` | 跟随 `PY_PORT` | 写入 M3U 的外部虎牙端口 |

“内部端口”是容器进程监听值，“公开端口”是播放器连接的宿主机值。二者不一致却没有设置 `PUBLIC_*` 时，M3U 能下载但频道会连错端口。

## 直播目录同步

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `SYNC_HUYA_PAGES` | `15` | 虎牙最多抓取页数 |
| `SYNC_DOUYU_PAGES` | `20` | 斗鱼最多抓取页数 |
| `SYNC_MIN_HUYA` | `20` | 少于此房间数时保留旧列表 |
| `SYNC_MIN_DOUYU` | `20` | 少于此房间数时保留旧列表 |

同步任务每 6 小时执行一次。设置过大的最低数量可能导致平台冷门时段永远无法首次生成列表；最低数量只负责防止错误响应覆盖旧文件，真正的可播放检测由后续健康检查完成。

## 流代理参数

| 变量 | 默认值 | 说明 |
|---|---:|---|
| `UPSTREAM_PROXY` | 空 | Python 拉流使用的 HTTP/SOCKS 代理 URL |
| `IPV6_PLATFORMS` | 空 | 逗号分隔，指定哪些平台让 Python curl 强制 IPv6 |
| `HLS_IDLE` | `300` | HLS 无访问多少秒后回收 FFmpeg |
| `HLS_STALE` | `25` | 播放列表多久不更新视为卡死 |
| `HLS_WARMUP` | `45` | FFmpeg 首次生成 HLS 的最长宽限时间 |

例如只让某些平台通过 IPv6：

```dotenv
IPV6_PLATFORMS=huya,douyu
```

不要盲目强制 IPv6；宿主机、Docker 网络和上游 CDN 都必须具备可用 IPv6。

## `iptv-api` 配置

首次启动会把上游默认配置复制到：

```text
<LIVETV_DATA_DIR>/iptv-api/config/
```

常见文件：

- `config.ini`：更新时间、并发、测速、分辨率、EPG 等。
- `subscribe.txt`：订阅来源。
- `blacklist.txt`：上游抓取阶段的 URL 黑名单。
- `whitelist.txt`：白名单。

默认启用可播放性、速度、最低分辨率和广告占位过滤，并按 12 小时间隔更新。修改后重启容器让长期更新进程重新加载配置：

```bash
docker compose -f docker/docker-compose.yml restart livetv
```

本项目还有发布前的 `iptv-blocklist.txt`。两类黑名单区别是：

| 文件 | 生效阶段 | 适合用途 |
|---|---|---|
| `iptv-api/config/blacklist.txt` | 收集和测速前 | 减少无用请求 |
| `lnmp/applecms/iptv-blocklist.txt` | 发布最终快照前 | 最后一道录播/坏源保护 |

## 可选 NAS 同步

只有三个变量同时设置才会启用：

| 变量 | 是否必填 | 说明 |
|---|---|---|
| `NAS_HOST` | 是 | NAS 域名或 IP |
| `NAS_USER` | 是 | SSH 用户 |
| `NAS_M3U` | 是 | NAS 上 M3U 的绝对路径 |
| `DST_FILE` | 否 | 本地目标文件 |
| `MIN_LINES` | 否 | 最低频道数，默认 20 |

认证使用部署环境已有的 SSH 配置/密钥，不要把私钥写进镜像、Compose 或 Git 仓库。首次连接应核对主机指纹。
