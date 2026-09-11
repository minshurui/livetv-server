# Docker、群晖与 OpenWrt 部署白皮书

这不是参数清单，而是一份从空目录开始的部署手册。照着完成后，你会得到一个固定的 M3U 地址，容器在后台自动更新直播目录、检测 IPTV、过滤明显录播，并保存最后一次可用结果。

## 部署完成后会得到什么

默认地址如下：

| 地址 | 用途 |
|---|---|
| `http://服务器地址:8081/allinone.m3u` | 播放器唯一需要添加的聚合列表 |
| `http://服务器地址:8081/healthz` | 检查聚合入口是否正常 |
| `http://服务器地址:8080/` | IPTV 管理和结果页面，可选 |

M3U 中的虎牙和斗鱼不是固定 CDN 地址，而是本机代理地址。播放器播放频道时还会连接：

- `19090`：虎牙/斗鱼 Go 续流代理；
- `19091`：旧版虎牙直通兼容，新部署不是必需。

因此只开放 `8081` 会出现“列表能下载、直播却打不开”。

## 部署前检查

最低要求：

- amd64/x86-64 或 arm64；
- Docker Engine 24+；
- Docker Compose v2，命令为 `docker compose`；
- 至少 1 GB 内存，IPTV 大量测速建议 2 GB 以上；
- 数据目录至少预留 2 GB；
- 服务器能访问 Docker Hub、虎牙、斗鱼和你的 IPTV 订阅。

检查命令：

```bash
docker version
docker compose version
uname -m
df -h
```

架构对应关系：

| `uname -m` | 镜像架构 | 是否支持 |
|---|---|---|
| `x86_64` | `linux/amd64` | 支持 |
| `aarch64` / `arm64` | `linux/arm64` | 支持 |
| `armv7l` | `linux/arm/v7` | 暂不支持 |

## 方案 A：普通 Linux 一键部署

适用于 Debian、Ubuntu、CentOS 等已经安装 Docker 的服务器。

### 第一步：创建独立目录

```bash
sudo mkdir -p /opt/livetv
sudo chown -R "$USER":"$USER" /opt/livetv
cd /opt/livetv
nano compose.yml
```

如果你习惯放在其他磁盘，可以把 `/opt/livetv` 换成自己的目录。路径只存在于部署机器，不要写回 GitHub 仓库。

### 第二步：粘贴 Compose

```yaml
services:
  livetv:
    image: minshurui/livetv-allinone:latest
    container_name: livetv
    restart: unless-stopped
    init: true
    ports:
      - "8081:8081"   # 最终播放列表
      - "19090:19090" # 虎牙/斗鱼直播流
      - "19091:19091" # 可选：旧版虎牙链接兼容
      - "8080:8080"   # IPTV 页面；不需要可删除
    environment:
      TZ: Asia/Shanghai
      PUBLIC_HOST: ""
      HUYA_CDN: AL
      HUYA_CODEC: "264"
      IPTV_REJECT_VOD: "1"
      IPTV_MIN_CHANNELS: "20"
    volumes:
      - ./data:/data
    security_opt:
      - no-new-privileges:true
    stop_grace_period: 20s
    logging:
      options:
        max-size: "10m"
        max-file: "3"
```

### 第三步：拉取并启动

```bash
docker compose pull
docker compose up -d
docker compose ps
```

不要使用 `docker compose up -d --build`，除非你明确想从源码构建。普通部署直接使用已发布镜像更快、更稳定。

### 第四步：看首次启动

```bash
docker logs --tail=150 -f livetv
```

正常启动会经历：

1. 初始化 `/data` 目录；
2. 启动 Go 服务、虎牙代理、nginx 和 `iptv-api`；
3. 同步虎牙/斗鱼当前直播目录；
4. `iptv-api` 下载候选电视源并测速；
5. 过滤器验证新列表，满足频道下限后原子发布。

按 `Ctrl+C` 退出日志即可，容器继续运行。不要因为 IPTV 还没生成就反复重启，否则每次都会打断首次测速。

### 第五步：验证

```bash
curl -fsS http://127.0.0.1:8081/healthz && echo
curl -fsS http://127.0.0.1:8081/allinone.m3u | sed -n '1,12p'
docker inspect --format '{{.State.Health.Status}}' livetv
```

预期：

- 健康接口输出 `ok`；
- 列表第一行是 `#EXTM3U`；
- 健康状态最终为 `healthy`。

最后从另一台设备测试：

```text
http://服务器局域网IP:8081/allinone.m3u
```

服务器本机能访问、电视不能访问，通常是防火墙、端口映射或网络隔离，而不是容器内部故障。

## 部署验收清单

不要只看容器“正在运行”。按下面顺序验收，全部通过才算真正可用。

### 1. 核心进程和入口

```bash
docker compose ps
docker inspect --format '{{.State.Health.Status}}' livetv
curl -fsS http://127.0.0.1:8081/healthz && echo
```

结果：容器为 `Up`，健康状态为 `healthy`，接口输出 `ok`。

### 2. M3U 格式和频道数量

```bash
curl -fsS http://127.0.0.1:8081/allinone.m3u -o /tmp/allinone.m3u
head -n 5 /tmp/allinone.m3u
grep -c '^#EXTINF:' /tmp/allinone.m3u
```

结果：第一行是 `#EXTM3U`，频道数量大于 0。首次 IPTV 尚未完成时，频道数会继续增加。

### 3. M3U 没有写入回环地址

从另一台设备用服务器局域网 IP 下载 M3U，再检查频道 URL：

```bash
curl -fsS http://服务器局域网IP:8081/allinone.m3u | grep -m 3 '^http'
```

结果：URL 使用服务器可达地址，不应错误写成 `127.0.0.1`。

### 4. 平台代理端口可达

在播放器所在电脑测试：

```bash
curl -I --max-time 5 http://服务器局域网IP:19090/
```

即使根路径返回 404，只要不是连接超时或拒绝，就说明端口已到达服务。真正播放仍需使用 M3U 中的完整频道路径。只有验证旧版虎牙链接时才额外测试 19091。

### 5. IPTV 原始结果和最终快照

```bash
docker exec livetv ls -lh /data/iptv-api/output/result.m3u
docker exec livetv ls -lh /data/lnmp/applecms/.iptv-result.m3u
docker exec livetv tail -n 30 /data/lnmp/logs/bridge.log
```

首次测速未结束时可以暂时没有文件。生成后，桥接日志应说明保留、过滤和发布了多少频道。

### 6. 持久化和重启

```bash
docker compose restart livetv
docker compose ps
ls -la data/iptv-api/config
ls -la data/lnmp
```

结果：重启后配置、频道目录和 last-good 快照仍在。

### 7. 最终播放器验证

播放器添加：

```text
http://服务器局域网IP:8081/allinone.m3u
```

分别抽查一个虎牙、斗鱼和 IPTV 频道。某个房间离线返回 404 属于正常状态；大量频道同一时间失败才需要继续排查网络或端口。

## 方案 B：群晖 Container Manager

适用于 DSM 7。建议使用“项目”而不是手动创建单个容器，这样升级和迁移更容易。

### 用 SSH 创建项目目录

以下使用 `/volume1/docker/livetv`，可按自己的存储卷修改：

```bash
mkdir -p /volume1/docker/livetv/data
cd /volume1/docker/livetv
nano compose.yml
```

粘贴“方案 A”的 Compose。相对挂载 `./data:/data` 会保存到：

```text
/volume1/docker/livetv/data
```

然后：

```bash
docker compose pull
docker compose up -d
```

### 完全使用图形界面

1. 打开 **Container Manager → 项目 → 新增**。
2. 项目名称填写 `livetv`。
3. 来源选择“创建 docker-compose.yml”。
4. 路径选择一个空目录，例如 `/volume1/docker/livetv`。
5. 粘贴“方案 A”的 Compose。
6. 启动项目，等待容器显示 `healthy`。
7. 浏览器打开 `http://群晖IP:8081/healthz`。
8. 播放器添加 `http://群晖IP:8081/allinone.m3u`。

群晖防火墙启用时，至少允许播放器所在局域网访问 TCP `8081`、`19090`。只有旧版虎牙链接才需要 `19091`；`8080` 只允许管理设备访问。

## 方案 C：OpenWrt / iStoreOS

路由器上最常见的问题不是项目本身，而是空间、内存和端口冲突。

部署前执行：

```bash
docker version
docker compose version
df -h
free -h
docker ps --format 'table {{.Names}}\t{{.Ports}}'
```

建议把数据放在外接磁盘，例如：

```bash
mkdir -p /mnt/docker/livetv/data
cd /mnt/docker/livetv
nano compose.yml
```

如果 `8080` 已被路由器管理服务占用，把 Compose 改为：

```yaml
ports:
  - "8201:8081"
  - "29090:19090"
  - "8200:8080"
environment:
  PUBLIC_HOST: ""
  PUBLIC_PROXY_PORT: "29090"
```

此时播放地址是：

```text
http://路由器地址:8201/allinone.m3u
```

低内存设备不建议设置很高的 IPTV 并发。完整电视源测速不适合空间很小的路由器；可把服务部署在 NAS，路由器只负责网络。

## 使用仓库自带 Compose

如果需要修改源码或使用 `.env` 管理端口：

```bash
git clone https://github.com/minshurui/livetv-server.git
cd livetv-server
cp docker/.env.example docker/.env
nano docker/.env
docker compose -f docker/docker-compose.yml pull
docker compose -f docker/docker-compose.yml up -d --no-build
```

`.env` 已加入 `.gitignore`。真实 IP、域名、代理和私有路径只写在部署机器的 `docker/.env`，不要提交。

## 端口冲突时怎么改

### 只修改 M3U 入口

单文件 Compose：

```yaml
ports:
  - "8201:8081"
```

入口变成：

```text
http://服务器地址:8201/allinone.m3u
```

仓库 `.env`：

```dotenv
HOST_LIVETV_PORT=8201
```

### 修改虎牙/斗鱼代理端口

单文件 Compose 必须同时修改映射和 M3U 中公开端口：

```yaml
ports:
  - "29090:19090"
environment:
  PUBLIC_PROXY_PORT: "29090"
```

仓库 `.env` 只需：

```dotenv
HOST_PROXY_PORT=29090
```

仓库 Compose 会自动把 `HOST_*` 传给 `PUBLIC_*`。

## IP、域名、IPv6 与反向代理

### 局域网 IP

保持 `PUBLIC_HOST` 为空。你用哪个地址下载 M3U，列表就使用哪个 Host：

```text
http://192.168.1.50:8081/allinone.m3u
```

### 固定域名

只有反向代理没有保留原始 Host，或你必须强制固定域名时才设置：

```yaml
environment:
  PUBLIC_HOST: tv.example.com
```

不要填写 `http://`、端口或路径。

### IPv6

直接用 IPv6 地址访问时：

```text
http://[你的IPv6地址]:8081/allinone.m3u
```

程序会自动给 M3U 中的 IPv6 字面量添加方括号。Docker 宿主机、容器网络、客户端和中间防火墙都必须支持 IPv6；仅宿主机有 IPv6 不代表容器上游一定可用。

### 公网访问

项目没有账号密码功能。若需要公网访问：

- 优先使用 WireGuard、Tailscale 等 VPN；
- 或使用带认证的反向代理保护 `8081`；
- 不要公开 `8080` 管理页；
- `19090` 是播放器直连的新版流端口，无法只反代 M3U 就自动保护；`19091` 仅用于旧虎牙链接。

## 首次启动要等多久

| 内容 | 通常出现时间 | 失败时查看 |
|---|---|---|
| `/healthz` | 数十秒 | `docker logs livetv` |
| 虎牙/斗鱼目录 | 约 1–5 分钟，受平台网络影响 | `sync-channels.log` |
| 斗鱼存活白名单 | 首次健康检查完成后 | `livetv.log` |
| IPTV 电视源 | 几分钟到几十分钟 | `iptv-api-update.log`、`bridge.log` |

实时查看：

```bash
docker exec livetv tail -f /data/lnmp/logs/sync-channels.log
docker exec livetv tail -f /data/lnmp/logs/iptv-api-update.log
docker exec livetv tail -f /data/lnmp/logs/bridge.log
```

## 修改 IPTV 来源与过滤

首次启动会把可编辑配置放到宿主机：

```text
./data/iptv-api/config/
```

常用文件：

- `subscribe.txt`：候选订阅地址；
- `config.ini`：测速、清晰度、并发、更新时间、EPG；
- `blacklist.txt`：在 IPTV 收集阶段提前排除 URL；
- `whitelist.txt`：上游白名单。

编辑后重启长期更新进程：

```bash
docker compose restart livetv
```

项目还有最后一道持久化过滤：

```bash
nano data/lnmp/applecms/iptv-blocklist.txt
docker exec livetv /opt/livetv/scripts/bridge_iptv.sh
```

每行一个 URL 关键字，适合排除已经人工确认的循环录播域名或路径。

## 日常管理

```bash
# 状态
docker compose ps

# 主日志
docker logs --tail=200 livetv

# 实时日志
docker logs -f livetv

# 重启
docker compose restart livetv

# 停止并保留容器
docker compose stop

# 删除容器但保留 ./data
docker compose down
```

## 升级、备份与回滚

### 升级

```bash
cd /opt/livetv
docker compose pull
docker compose up -d --remove-orphans
docker compose ps
```

### 备份

重要数据全在 `data/`。先停止写入再打包最稳妥：

```bash
docker compose stop
tar -czf "livetv-data-$(date +%F).tar.gz" data
docker compose start
```

备份文件可能包含私人订阅地址，不要上传到公开仓库。

### 固定版本

`latest` 会跟随最新成功构建。稳定部署可以改为提交标签：

```yaml
image: minshurui/livetv-allinone:完整Git提交SHA
```

### 回滚

把 `image:` 改回之前可用的 SHA 标签，然后：

```bash
docker compose pull
docker compose up -d --force-recreate
```

数据格式尽量保持兼容，但回滚前仍建议备份 `data/`。

## 本地构建和多架构构建

普通用户不需要执行本节。

单架构测试：

```bash
git clone https://github.com/minshurui/livetv-server.git
cd livetv-server
docker build -t livetv-allinone:test .
docker run -d --name livetv-test \
  -p 8081:8081 -p 19090:19090 \
  -v "$PWD/test-data:/data" \
  livetv-allinone:test
```

多架构发布：

```bash
docker buildx create --name livetv-builder --use 2>/dev/null || \
  docker buildx use livetv-builder
docker run --privileged --rm tonistiigi/binfmt --install amd64,arm64
docker buildx build \
  --platform linux/amd64,linux/arm64 \
  -t your-name/livetv-allinone:latest \
  --push .
```

多架构镜像不能同时 `--load` 到本机。只验证时使用 `--output type=cacheonly`，发布时使用 `--push`。

## 下一步

- 所有环境变量：[配置参考](configuration.md)
- 不知道某个进程在做什么：[架构说明](architecture.md)
- 容器异常、列表空或播放失败：[故障排查](troubleshooting.md)
- Android 精简运行：[Termux 部署](termux.md)
