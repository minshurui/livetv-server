# Docker 部署

## 使用发布镜像

```bash
cp docker/.env.example docker/.env
docker compose -f docker/docker-compose.yml up -d
docker compose -f docker/docker-compose.yml ps
```

`LIVETV_DATA_DIR` 控制宿主机数据目录；默认是仓库根目录的 `data/`。不要把个人地址和密钥写进 Compose，使用未提交的 `docker/.env` 或部署平台的环境变量。

## 本地构建

Dockerfile 需要 `guovern/iptv-api` 作为构建上下文中的 `src/iptv-api`：

```bash
git clone --depth 1 https://github.com/guovern/iptv-api.git src/iptv-api
docker compose -f docker/docker-compose.yml build
docker compose -f docker/docker-compose.yml up -d
```

可选代理必须显式传入：`docker build --build-arg BUILD_PROXY=http://proxy.example:7890 -t livetv .`。默认不使用代理。

## amd64 / arm64

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t minshurui/livetv-allinone:latest --push .
```

工作流会先运行 Go/Python/Shell 静态验证，再构建 amd64 与 arm64 镜像；推送镜像仅在相应 Secrets 已配置时执行。端口为 35455、19090、35456、19091、8081、8080；通常只需对外开放 8081。
