# 架构

`livetv` 提供两个 Go HTTP 服务：`AIO_PORT`（默认 35455）负责 M3U 与房间解析，`PROXY_PORT`（默认 19090）负责斗鱼 FLV/HLS。Python 服务分别处理虎牙签名解析（35456）和 FLV 直通（19091）。容器中的 nginx 在 8081 暴露稳定的 M3U 入口。

运行数据全部位于 `DATA/lnmp`：频道清单在 `allinone/channels.json`，电视快照在 `applecms/.iptv-result.m3u`，日志在 `logs/`。容器默认 `DATA=/data`，因此挂载一个 `/data` 卷即可持久化。

M3U 请求优先使用请求里的 Host 生成各条播放 URL，因此同一个服务可从不同局域网地址访问。若前置代理不传递 Host，可通过 `PUBLIC_HOST` 指定公开域名或 IP。
