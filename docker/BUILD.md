# 构建说明 — 在 WSL Ubuntu 上多架构构建 Docker 镜像

把 `livetv` 仓库 (含根目录 Dockerfile) 放到 **WSL Ubuntu** 或任何有 Docker 的 Linux,
构建直播全链路 + 电视源自愈的 **单 Alpine 容器**。

## 前置: 先 clone iptv-api (开源电视源聚合, 构建必需)
```bash
# 在 WSL Ubuntu / 或你有网络的本机
git clone --depth 1 https://github.com/guovern/iptv-api.git src/iptv-api
```
> 需要 `src/iptv-api` 存在 (提供 Pipfile + 源码 + nginx.conf.template)。
> 已在仓库 `.gitignore` 排除, 不入 GitHub。

## 构建
```bash
# 装 docker + buildx + qemu(多架构)
sudo apt install docker.io docker-compose
docker buildx version
docker run --privileged --rm tonistiigi/binfmt --install all   # arm 模拟

# 单架构(最快)
docker build -t minshurui/livetv-allinone:latest .

# 多架构
docker buildx create --name multiarch --driver docker-container --use
docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7 \
  -t minshurui/livetv-allinone:latest --push .
```

## 运行
```bash
docker compose -f docker/docker-compose.yml up -d
# 或直接:
docker run -d --name livetv -p 35455:35455 -p 19090:19090 -p 35456:35456 \
  -p 19091:19091 -p 8081:8081 -p 8080:8080 -v ./data:/data \
  minshurui/livetv-allinone
```

## 验证
```bash
curl http://localhost:8081/healthz        # → ok
curl http://localhost:8081/allinone.m3u   # → 聚合 m3u(虎牙/斗鱼/电视)
curl http://localhost:8080/               # → iptv-api Web UI
```

## 端口
| 端口 | 服务 |
|---|---|
| 35455 | Go livetv 解析/m3u |
| 19090 | Go 斗鱼 FLV |
| 35456 | Python 虎牙 301 解析 |
| 19091 | Python 虎牙 FLV 直通 |
| 8081 | 自研 nginx 反代 allinone.m3u |
| 8080 | iptv-api Web UI (电视源) |
| 5180/1935/80 | iptv-api 内部/rtmp |

## 数据卷
`./data:/data` → channels.json、电视源(.iptv-result.m3u)、日志
电视源自愈: iptv-api 生成 result.m3u → 每15min 桥接复制到 Go 可读位置
