# 构建说明 — WSL Ubuntu 上双架构构建

把 `livetv` 仓库放到 **WSL Ubuntu**,构建"直播全链路 + 电视源自愈"的 **单 Alpine 容器**。
镜像支持 **amd64 (x86_64) + arm64** 双端,任何有 Docker 的平台都能跑。

## 前提: clone iptv-api (构建必需)
```bash
git clone --depth 1 https://github.com/guovern/iptv-api.git src/iptv-api
```
> 已入 `.gitignore`,不入 GitHub。需要其 Pipfile + 源码 + nginx.conf.template。

## 构建环境准备 (WSL Ubuntu)
```bash
sudo apt install docker.io docker-compose
sudo systemctl enable docker --now || sudo service docker start
docker buildx version          # v0.15+ 有 multi-platform
docker run --privileged --rm tonistiigi/binfmt --install amd64,arm64   # QEMU 交叉
echo | docker buildx create --name multiarch --driver docker-container --use
```

## 双端版构建 (amd64 + arm64, 一次出两镜像)
```bash
# 完整功能(含 ffmpeg)
docker buildx build --platform linux/amd64,linux/arm64 \
  -t minshurui/livetv-allinone:latest \
  -t minshurui/livetv-allinone:amd64 \
  -t minshurui/livetv-allinone:arm64 --push .

# 裁剪版(无 ffmpeg, 省 ~80MB; 不含 HLS 转码/源截图测速)
docker buildx build --platform linux/amd64,linux/arm64 \
  --build-arg WITH_FFMPEG=0 \
  -t minshurui/livetv-allinone:slim --push .
```

## 单架构快速验证 (仅 amd64)
```bash
docker build -t minshurui/livetv-allinone:test .
docker run -d --name t -p 8081:8081 -p 8080:8080 minshurui/livetv-allinone:test
curl localhost:8081/healthz
```

## 部署
```bash
docker compose -f docker/docker-compose.yml up -d
# docker pull 会自动按平台拉 amd64 或 arm64
```

## 端口
| 端口 | 服务 |
|---|---|
| 35455 | Go livetv 解析/m3u |
| 19090 | Go 斗鱼 FLV |
| 35456 | Python 虎牙 301 解析 |
| 19091 | Python 虎牙 FLV 直通 |
| 8081 | 自研 nginx 反代 allinone.m3u |
| 8080 | iptv-api Web UI |

## 体积
| 版本 | 预估 |
|---|---|
| latest (含 ffmpeg) | ~250MB |
| slim (WITH_FFMPEG=0) | ~170MB |

## 数据卷
`./data:/data` → channels.json、电视源、日志。iptv-api 生成 result.m3u 每 15min 桥接给 Go。
