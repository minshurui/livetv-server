# livetv-server 文档中心

这里不是把参数堆在一起，而是按“先部署、再验证、再理解、最后定制”的顺序组织。

## 第一次使用怎么读

1. 先读 [Docker、群晖与 OpenWrt 部署白皮书](docker.md)，完成部署和验收。
2. 端口、域名或过滤规则需要修改时查 [配置参考与实用模板](configuration.md)。
3. 想知道直播源怎样生成、怎样判断录播，读 [系统架构与直播源判断白皮书](architecture.md)。
4. 出现红字、空列表或频道打不开，按 [故障排查手册](troubleshooting.md) 的症状索引处理。
5. Android 手机运行精简版，直接读 [Termux 部署手册](termux.md)。

## 按设备选择

| 设备 | 推荐方案 | 需要的端口 | 从这里开始 |
|---|---|---|---|
| Debian / Ubuntu / 普通 Linux | Docker Compose | 8081、19090、19091；8080 可选 | [普通 Linux](docker.md#方案-a普通-linux一键部署) |
| 群晖 DSM 7 | Container Manager 项目 | 8081、19090、19091；8080 可选 | [群晖](docker.md#方案-b群晖-container-manager) |
| OpenWrt / iStoreOS | 外接磁盘上的 Docker Compose | 建议先改掉冲突端口 | [OpenWrt](docker.md#方案-copenwrt--istoreos) |
| Android | Termux 精简模式 | 35455、19090、19091 | [Termux](termux.md) |

## 按问题选择

| 你现在的问题 | 对应章节 |
|---|---|
| 不知道复制哪份 Compose | [普通 Linux 一键部署](docker.md#方案-a普通-linux一键部署) |
| 群晖图形界面怎样填 | [群晖 Container Manager](docker.md#方案-b群晖-container-manager) |
| 8080/8081 已被占用 | [端口冲突](docker.md#端口冲突时怎么改) |
| 列表能下载但直播打不开 | [列表能下载但频道播放失败](troubleshooting.md#列表能下载但频道播放失败) |
| IPTV 一直没有生成 | [IPTV 电视源一直没有加入](troubleshooting.md#iptv-电视源一直没有加入) |
| 列表中出现录播 | [仍然出现录播或循环频道](troubleshooting.md#仍然出现录播或循环频道) |
| M3U 写入了错误 IP/端口 | [Host、域名和 IPv6](configuration.md#host域名和-ipv6) |
| GitHub Actions 红叉 | [构建或发布失败](troubleshooting.md#github-actions-构建或发布失败) |
| 需要升级、备份或回滚 | [升级、备份与回滚](docker.md#升级备份与回滚) |

## 最小成功标准

一次完整部署至少满足：

- `http://服务器:8081/healthz` 返回 `ok`；
- `allinone.m3u` 第一行是 `#EXTM3U`；
- 容器状态最终为 `healthy`；
- 播放器所在设备能访问 8081、19090、19091；
- 重建容器后 `data/` 中配置和最后可用列表仍存在；
- 离线房间返回 404，而不是播放测试录像；
- 新 IPTV 结果异常时不会覆盖 last-good 快照。

完整验收命令见 [部署验收清单](docker.md#部署验收清单)。

## 项目能力边界

项目擅长识别：

- 失效 URL、超时和空响应；
- 返回 HTML 的假视频地址；
- 已下播或解析失败的平台房间；
- MP4/MKV 等文件型点播；
- M3U 中明确声明正时长的 VOD；
- 用户已经确认并加入黑名单的域名或路径。

项目无法保证自动识别：

- 一直生成新 HLS 分片的长循环录像；
- 内容本身是否为“实时画面”；
- 平台临时风控、地区限制和运营商网络封锁；
- 第三方订阅内容的合法性、稳定性和真实性。

详细原理见 [直播源判断边界](architecture.md#真直播到底能判断到什么程度)。

## 配置原则

- 镜像和仓库只保存通用默认值；
- 真实 IP、域名、NAS 路径和代理放在部署机器 `.env`；
- Docker Hub、阿里云和 GitHub 凭证只放 Secrets；
- 外部订阅和运行数据只保存在 `/data`；
- 所有新结果先验证，再原子替换；
- 新结果低于保护下限时保留 last-good。

## 发布渠道

- GitHub：<https://github.com/minshurui/livetv-server>
- Docker Hub：<https://hub.docker.com/r/minshurui/livetv-allinone>
- GitHub Actions：<https://github.com/minshurui/livetv-server/actions/workflows/build-image.yml>

每次主线发布先执行测试，再分别验证 amd64/arm64，最后推送 Docker Hub 和配置完整时的阿里云 ACR。
