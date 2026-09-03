# livetv — 直播聚合服务（Termux/Android 单二进制 + Python 辅助）

在安卓手机 Termux 上运行的全链路直播聚合服务。虎牙/斗鱼直播 + 央视/卫视电视源的
聚合、代理、自愈。Go 主服务 + Python 辅助（虎牙流 + 电视源自愈）。

## 整体架构（2026-09 分流版）

```
用户播放器 (TVBox/VLC)
      │  http://<host>:8081/allinone.m3u  (nginx 反代 → livetv 35455)
      ▼
   ┌──────────────────────────────────────────────┐
   │ livetv (Go, 单二进制)                          │
   │  35455 解析/线路m3u/聚合m3u                     │
   │  19090 斗鱼 FLV 代理                            │
   └──────────────────────────────────────────────┘
      │ 虎牙URL→19091(Python)         │电视源(.iptv-result.m3u 快照)
      ▼                              ▼
  stream-proxy.py (Python)      iptv-api (NAS, 自愈管道)
  FLV 直通, 断流自行续播         每30min从公网源聚合, 剔除录播/错配
```

### 电视直播源自愈（本次重点）
- NAS 上跑 `guovern/iptv-api` 开源管道：从公网订阅源聚合央视/卫视，每 30 分钟自愈刷新。
- 手机端 `sync_iptv_from_nas.sh` 每 30 分钟免密 SSH 拉取 NAS 干净源，原子替换本地
  电视快照，带 last-good 保护（拉取失败保留旧文件）。
- 通过 iptv-api 黑名单剔除录播源（`txmov2.a.kwimgs.com`/`r.jdshipin.com` 等），
  关键台在白名单（local.txt + `$!`）补官方直播源。

## 端口
| 端口 | 功能 |
|---|---|
| 35455 | Go 解析器 + 线路 m3u + 聚合 m3u |
| 19090 | Go 斗鱼 FLV 代理 |
| 19091 | Python 虎牙 FLV 直通（断流 1s 自行续播） |
| 35456 | Python 虎牙 301 签名解析 |
| 8081 | nginx 反代 allinone.m3u（用户入口） |

## 分流规则
| 平台 | 经过 | 说明 |
|---|---|---|
| 虎牙 | Python 19091 | FLV 直通，播放器自行处理断流续播 |
| 斗鱼 | Go 19090 | 一直正常 |
| 央视/卫视 | NAS iptv-api → 手机快照 | 自愈同步，剔除录播/错配源 |

## 数据文件
- `channels.json` — 频道列表
- `.iptv-result.m3u` — 电视快照（追加进聚合 m3u，由 sync 脚本维护）
- `douyu-alive.txt` / `.douyu-health.stamp` — 斗鱼白名单

## 环境变量（默认值）
`PUBLIC_HOST=192.168.1.95  AIO_PORT=35455  PROXY_PORT=19090  PY_PORT=19091  RES_PORT=35456`

## 自愈脚本
```bash
bash ~/lnmp/scripts/sync_iptv_from_nas.sh   # 电视源自愈同步（NAS→手机）
python3 ~/lnmp/scripts/filter_live_iptv.py  # 剔除录播/死源筛选
python3 ~/lnmp/scripts/detect_vod_domains.py# 批量检测源直播性
```

## 运维
```bash
bash start-all.sh start        # 全量启动（含 Python 辅助）
tail -f ~/lnmp/logs/livetv.log # Go 日志
tail -f ~/lnmp/logs/iptv-sync.log # 电视源自愈日志
```

---

## 🐳 Docker 单容器部署 (livetv-allinone)

一键打包全部服务进单个 Alpine 容器，支持 amd64/arm64 (任何有 Docker 的平台)。

整合: 自研 Go livetv + Python 虎牙 + 反代 nginx + 开源 guovern/iptv-api(电视源自愈)。

### 方式 A: GitHub Actions 自动构建(推荐)

仓库已配置 [`build-image.yml`](.github/workflows/build-image.yml) 工作流,推 `main` 或手动触发即**自动构建 amd64+arm64 双端镜像**并推送到:

- **Docker Hub** `minshurui/livetv-allinone:latest`
- **阿里云 ACR**(需配置下列 Secrets 才推)

首次使用需在 GitHub → Settings → Secrets 配置:
```
DOCKERHUB_USERNAME / DOCKERHUB_TOKEN        # Docker Hub 访问令牌
ALIYUN_REGISTRY / ALIYUN_NAMESPACE / ALIYUN_REPO
ALIYUN_USERNAME / ALIYUN_PASSWORD           # 阿里云容器镜像(可选)
```
配好后点 Actions → Run workflow 即自动构建,无需本地 Docker。

### 方式 B: 本地构建(备选)

```bash
# 1. 前置: clone iptv-api (构建依赖)
git clone --depth 1 https://github.com/guovern/iptv-api.git src/iptv-api

# 2. 在 WSL Ubuntu(或任何有 docker 的机器) 构建
docker build -t minshurui/livetv-allinone:latest .
# 双架构见 docker/BUILD.md

# 3. 运行
docker compose -f docker/docker-compose.yml up -d
```

端口映射: 35455(Go m3u) 19090(斗鱼FLV) 35456/19091(虎牙) 8081(m3u反代) 8080(iptv-api UI)

详见 [`docker/BUILD.md`](docker/BUILD.md)。
