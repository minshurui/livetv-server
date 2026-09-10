# 镜像维护与发布

普通用户请看 [Docker 部署文档](../docs/docker.md)。本文件面向镜像维护者。

## 当前构建策略

Dockerfile 分为两层：

1. `golang:1.26-alpine` 运行 Go 测试并编译静态二进制。
2. 固定 digest 的 `guovern/iptv-api` 多架构镜像提供 Python 依赖、FFmpeg 和 nginx-rtmp，再复制本项目服务。

这样避免在 arm64 QEMU 环境中重复编译 nginx-rtmp，也避免每次构建从 Alpine/PyPI 下载大量编译依赖。上游基础镜像 digest 同时包含 amd64、arm64 和 arm/v7；本项目正式发布 amd64、arm64。

## 单架构验证

```bash
docker build --pull -t livetv-allinone:test .
docker run -d --name livetv-test \
  -p 8081:8081 -p 19090:19090 -p 19091:19091 \
  -v "$PWD/test-data:/data" \
  livetv-allinone:test
docker inspect --format '{{.State.Health.Status}}' livetv-test
docker logs --tail=200 livetv-test
```

## 双架构验证

```bash
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  --output type=cacheonly .
```

不要对多架构构建使用 `--load`，本机 Docker 镜像存储不能一次加载多个平台。

## 发布

```bash
docker login
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t your-name/livetv-allinone:latest \
  -t your-name/livetv-allinone:$(git rev-parse HEAD) \
  --build-arg VCS_REF=$(git rev-parse HEAD) \
  --push .
```

仓库 GitHub Actions 在测试和两种架构验证都通过后执行同样的发布流程。

## 更新 `iptv-api` 基础镜像

不要直接把 Dockerfile 改回 `latest`。先确认上游 manifest 包含 amd64 和 arm64，再把 `IPTV_API_IMAGE` 更新为多架构 manifest digest：

```bash
docker buildx imagetools inspect guovern/iptv-api:latest
```

更新后必须执行：

```bash
docker buildx build --platform linux/amd64,linux/arm64 --output type=cacheonly .
```

也可以临时验证候选镜像而不改文件：

```bash
docker buildx build \
  --build-arg IPTV_API_IMAGE=guovern/iptv-api@sha256:<manifest-digest> \
  --platform linux/amd64,linux/arm64 \
  --output type=cacheonly .
```

## GitHub Secrets

Docker Hub 发布需要：

- `DOCKERHUB_USERNAME`
- `DOCKERHUB_TOKEN`

阿里云 ACR 为可选项：

- `ALIYUN_REGISTRY`
- `ALIYUN_NAMESPACE`
- `ALIYUN_REPO`
- `ALIYUN_USERNAME`
- `ALIYUN_PASSWORD`

Secrets 先映射到 job 级 `env`，`if` 只判断 `env.*`，避免 GitHub Actions 在不支持的表达式上下文中解析 `secrets.*`。任何曾公开发送、出现在日志或提交中的令牌都必须立即撤销。
