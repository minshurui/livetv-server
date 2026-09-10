# livetv-allinone

直播源聚合、自愈和明显录播/VOD 过滤，支持 Docker `linux/amd64` 与 `linux/arm64`。

项目源码与完整文档：<https://github.com/minshurui/livetv-server>

## 快速启动

创建 `compose.yml`：

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
```

启动：

```bash
docker compose pull
docker compose up -d
docker inspect --format '{{.State.Health.Status}}' livetv
```

播放器地址：

```text
http://服务器地址:8081/allinone.m3u
```

管理页面：

```text
http://服务器地址:8080/
```

首次电视源测速可能需要几分钟到几十分钟。虎牙/斗鱼目录会先在后台同步，不需要重复重启。

## 必须开放哪些端口

| 端口 | 用途 |
|---:|---|
| 8081 | 最终 M3U |
| 19090 | 斗鱼流代理 |
| 19091 | 虎牙流代理 |
| 8080 | 可选的 `iptv-api` 页面 |

项目没有内置登录认证，8080 不建议暴露到不可信公网。

## 数据

所有配置、结果和日志位于 `/data`。挂载 `./data:/data` 后，删除或升级容器不会丢失：

- `iptv-api/config/`：电视源订阅、测速和 EPG 设置
- `iptv-api/output/`：上游原始结果
- `lnmp/allinone/channels.json`：当前开播房间
- `lnmp/applecms/.iptv-result.m3u`：最终电视源快照
- `lnmp/applecms/iptv-blocklist.txt`：自定义录播/坏源 URL 黑名单
- `lnmp/logs/`：运行日志

## 录播过滤

镜像会过滤正时长 `EXTINF`、MP4/MKV 等明显 VOD 文件和用户黑名单。发现已知循环源后，把域名或路径写入：

```text
./data/lnmp/applecms/iptv-blocklist.txt
```

每行一个关键字。然后等待定时桥接，或执行：

```bash
docker exec livetv /opt/livetv/scripts/bridge_iptv.sh
```

持续更新分片的长循环视频在协议层可能与真直播相同，因此无法保证只靠自动网络探测 100% 识别。

## 查看日志

```bash
docker logs --tail=200 livetv
docker exec livetv tail -n 100 /data/lnmp/logs/iptv-api-update.log
docker exec livetv tail -n 100 /data/lnmp/logs/bridge.log
```

完整部署、端口修改、群晖教程和排障：<https://github.com/minshurui/livetv-server/tree/main/docs>。
