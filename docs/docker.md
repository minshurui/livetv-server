# Docker 与群晖部署

推荐使用发布镜像。镜像同时包含 Go 服务、虎牙代理、`iptv-api`、FFmpeg 和 nginx，宿主机不需要安装 Go、Python、npm 或 FFmpeg。

## 准备条件

- Docker Engine 24 或更新版本
- Docker Compose v2（命令是 `docker compose`）
- amd64/x86-64 或 arm64 设备
- 可访问直播平台和 IPTV 来源的网络
- 至少 1 GB 可用内存；大量测速时建议 2 GB 以上

确认环境：

```bash
docker version
docker compose version
uname -m
```

## 方法一：使用仓库 Compose

```bash
git clone https://github.com/minshurui/livetv-server.git
cd livetv-server
cp docker/.env.example docker/.env
nano docker/.env
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml up -d --no-build
```

检查：

```bash
docker compose -f docker/docker-compose.yml ps
docker inspect --format '{{.State.Health.Status}}' livetv
curl -fsS http://127.0.0.1:8081/healthz
curl -fsS http://127.0.0.1:8081/allinone.m3u | head
```

播放器地址：

```text
http://服务器地址:8081/allinone.m3u
```

## 方法二：只使用一份 Compose

不需要克隆源码时，创建 `compose.yml`：

```yaml
services:
  livetv:
    image: minshurui/livetv-allinone:latest
    container_name: livetv
    restart: unless-stopped
    init: true
    ports:
      - "8081:8081"
      - "19090:19090"
      - "19091:19091"
      - "8080:8080"
    environment:
      TZ: Asia/Shanghai
      PUBLIC_HOST: ""
      IPTV_REJECT_VOD: "1"
      IPTV_MIN_CHANNELS: "20"
    volumes:
      - ./data:/data
    security_opt:
      - no-new-privileges:true
    logging:
      options:
        max-size: "10m"
        max-file: "3"
```

然后执行：

```bash
docker compose pull
docker compose up -d
```

8081、19090、19091 是完整聚合列表实际需要的端口。8080 只用于查看 `iptv-api` 页面，不需要时可以删除该端口映射。35455 和 35456 是兼容/调试接口，也可以不映射。

## 群晖 Container Manager

以下路径只是示例，可换成你自己的共享文件夹：

```bash
mkdir -p /volume1/docker/livetv
cd /volume1/docker/livetv
nano compose.yml
docker compose pull
docker compose up -d
```

将上面的单文件 Compose 粘贴进去，并把：

```yaml
- ./data:/data
```

保留为相对目录即可。实际数据会落在 `/volume1/docker/livetv/data`。不要在 GitHub 仓库或 Compose 中写入公网 IP、SSH 密码或访问令牌。

若使用群晖图形界面：

1. 打开“Container Manager → 项目 → 新增”。
2. 项目路径选择一个空目录。
3. 粘贴 Compose 内容并构建项目。
4. 容器显示 `healthy` 后访问 `http://群晖地址:8081/allinone.m3u`。

端口被占用时修改宿主机左侧端口。例如改为 `8201:8081` 后，播放列表入口是 `:8201/allinone.m3u`；流代理端口若也修改，还要设置对应的公开端口：

```yaml
ports:
  - "29090:19090"
  - "29091:19091"
environment:
  PUBLIC_PROXY_PORT: "29090"
  PUBLIC_PY_PORT: "29091"
```

仓库自带的 Compose 使用 `HOST_PROXY_PORT`、`HOST_PY_PORT` 等变量时会自动完成这组对应关系。

## 首次启动过程

启动后不是立刻出现全部频道，顺序如下：

1. Go、Python、nginx 和 Web API 启动。
2. 后台下载虎牙/斗鱼当前开播目录。
3. 斗鱼逐步进行解析和拉流健康检查。
4. `iptv-api` 获取候选电视源并测速。
5. 过滤器剔除明显 VOD/黑名单项，频道数达到下限后原子发布。

查看进度：

```bash
docker logs --tail=100 -f livetv
docker exec livetv tail -f /data/lnmp/logs/iptv-api-update.log
docker exec livetv tail -f /data/lnmp/logs/sync-channels.log
docker exec livetv tail -f /data/lnmp/logs/bridge.log
```

按 `Ctrl+C` 只退出日志查看，不会停止容器。

## 自定义录播/坏源黑名单

编辑宿主机数据目录中的文件：

```bash
nano data/lnmp/applecms/iptv-blocklist.txt
```

每行一个不区分大小写的 URL 关键字：

```text
# 注释以 # 开头
example.invalid
/recorded/
```

等待下一次 15 分钟桥接，或立即执行：

```bash
docker exec livetv /opt/livetv/scripts/bridge_iptv.sh
```

## 升级与回滚

升级不会删除 `/data`：

```bash
docker compose pull
docker compose up -d --remove-orphans
docker image prune -f
```

生产环境建议把 `latest` 换成已验证的提交标签。例如 GitHub Actions 同时发布：

```text
minshurui/livetv-allinone:<Git提交SHA>
```

回滚时把 `LIVETV_IMAGE` 或 Compose 的 `image` 改回旧 SHA，再执行 `docker compose up -d`。

## 本地单架构构建

Dockerfile 直接复用固定 digest 的 `guovern/iptv-api` 多架构基础镜像，不再需要手动克隆 `src/iptv-api`：

```bash
docker build -t livetv-allinone:test .
docker run -d --name livetv-test \
  -p 8081:8081 -p 19090:19090 -p 19091:19091 \
  -v "$PWD/test-data:/data" \
  livetv-allinone:test
docker inspect --format '{{.State.Health.Status}}' livetv-test
```

## amd64/arm64 构建

```bash
docker buildx create --name livetv-builder --use 2>/dev/null || \
  docker buildx use livetv-builder
docker run --privileged --rm tonistiigi/binfmt --install amd64,arm64
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t your-name/livetv-allinone:latest \
  --push .
```

不能把多架构镜像同时 `--load` 到本机 Docker；只验证时使用 `--output type=cacheonly`，发布时使用 `--push`。

GitHub Actions 将 amd64 和 arm64 分开并行验证，全部通过后才发布多架构 manifest。构建页：<https://github.com/minshurui/livetv-server/actions/workflows/build-image.yml>。
