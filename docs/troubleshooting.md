# 排障

先检查容器状态和健康检查：

```bash
docker compose -f docker/docker-compose.yml ps
docker inspect --format '{{.State.Health.Status}}' livetv
curl -fsS http://127.0.0.1:8081/healthz
docker compose -f docker/docker-compose.yml logs --tail=200 livetv
```

若 M3U 内的链接地址不正确，确认前置 nginx/代理传递了 `Host`；否则设置 `PUBLIC_HOST=你的公开域名` 后重建容器。IPv6 地址会自动以 URL 所需的方括号形式写入播放地址。

若本地构建失败并提示 `src/iptv-api` 不存在，按 [Docker 文档](docker.md) 克隆该构建依赖。频道同步失败不会覆盖已有 `channels.json`；检查 `DATA/lnmp/logs/sync-channels.log`。NAS 同步失败时检查 `NAS_HOST`、`NAS_USER` 和已保存的 SSH 主机指纹。
