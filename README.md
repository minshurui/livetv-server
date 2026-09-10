# livetv-server

直播聚合服务：Go 负责 M3U 聚合、解析和斗鱼 FLV 代理；Python 辅助处理虎牙流与频道同步。支持 Termux 和 Docker（amd64/arm64）。

## 快速开始（Docker）

```bash
git clone https://github.com/minshurui/livetv-server.git
cd livetv-server
cp docker/.env.example docker/.env
docker compose -f docker/docker-compose.yml up -d
curl http://127.0.0.1:8081/healthz
```

播放器入口：`http://<服务器地址>:8081/allinone.m3u`。默认会按请求 Host 生成播放链接；仅在反向代理无法保留 Host 时才设置 `PUBLIC_HOST`。

本地构建前还需获取 `iptv-api` 依赖，详见 [Docker 部署](docs/docker.md)。

## 文档

- [架构](docs/architecture.md)
- [Docker 部署与多架构构建](docs/docker.md)
- [Termux 部署](docs/termux.md)
- [配置参考](docs/configuration.md)
- [排障](docs/troubleshooting.md)
- [免责声明](DISCLAIMER.md)

## 开发验证

```bash
go test ./...
python3 -m py_compile docker/*.py
shellcheck docker/entry.sh docker/scripts/*.sh  # 可选但推荐
```

请勿提交 `.env`、频道数据、日志、私有地址、SSH 信息或任何访问令牌。
