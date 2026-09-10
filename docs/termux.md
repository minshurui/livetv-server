# Termux 部署

Termux 模式运行 Go 聚合/斗鱼代理和 Python 虎牙代理。完整的 `iptv-api` 电视源测速栈更适合 Docker；Termux 可以使用直播平台频道，也可以从另一台设备同步已经生成的电视快照。

## 安装依赖

建议使用 F-Droid 或 GitHub 发布的新版 Termux：

```bash
pkg update
pkg install git golang python curl ffmpeg tmux openssh
termux-wake-lock
```

## 下载和编译

```bash
cd "$HOME"
git clone https://github.com/minshurui/livetv-server.git
cd livetv-server
go test ./...
go build -trimpath -ldflags '-s -w' -o livetv .
```

## 启动

项目脚本会启动两个必需进程，并同步一次虎牙/斗鱼当前开播目录：

```bash
cd "$HOME/livetv-server"
DATA="$HOME" bash switch.sh start
DATA="$HOME" bash switch.sh status
```

播放地址：

```text
http://手机局域网IP:35455/allinone.m3u
```

注意：Termux 模式没有 Docker 中的 8081 nginx，因此直接使用 35455。

停止和重启：

```bash
DATA="$HOME" bash switch.sh stop
DATA="$HOME" bash switch.sh restart
```

脚本从自身所在目录启动程序，不要求项目必须放在固定路径。PID、日志和频道数据分别保存在：

```text
$HOME/lnmp/run/
$HOME/lnmp/logs/
$HOME/lnmp/allinone/channels.json
```

## 查看日志

```bash
tail -f "$HOME/lnmp/logs/livetv.log"
tail -f "$HOME/lnmp/logs/huya-proxy.log"
tail -f "$HOME/lnmp/logs/sync-channels.log"
```

确认端口：

```bash
curl -fsS http://127.0.0.1:35455/allinone.m3u | head
curl -I --max-time 15 http://127.0.0.1:35455/douyu/房间号
```

离线房间应返回 404，不会再跳到测试录像。

## 开机或 Termux 启动时运行

Android 会限制后台进程。可以安装 Termux:Boot，然后创建：

```bash
mkdir -p "$HOME/.termux/boot"
nano "$HOME/.termux/boot/livetv.sh"
```

内容：

```bash
#!/data/data/com.termux/files/usr/bin/bash
termux-wake-lock
DATA="$HOME" bash "$HOME/livetv-server/switch.sh" start
```

赋权：

```bash
chmod +x "$HOME/.termux/boot/livetv.sh"
```

手机系统还需要允许 Termux 自启动，并关闭针对 Termux 的电池优化，否则后台服务可能被系统杀死。

## 定时刷新直播目录

`switch.sh start` 会同步一次。长期运行可安装 `cronie`：

```bash
pkg install cronie
crond
crontab -e
```

加入一行：

```cron
17 */6 * * * DATA="$HOME" python3 "$HOME/livetv-server/docker/sync_channels.py" --out "$HOME/lnmp/allinone/channels.json" >> "$HOME/lnmp/logs/sync-channels.log" 2>&1
```

## 可选：从 NAS 同步电视源

先确认 SSH 密钥登录可用，然后显式提供 NAS 地址、用户和文件路径：

```bash
NAS_HOST=nas.example.internal \
NAS_USER=your-user \
NAS_M3U=/path/to/iptv-api/output/result.m3u \
DATA="$HOME" \
bash docker/scripts/sync_iptv_from_nas.sh
```

脚本会校验频道数量后原子替换 `$HOME/lnmp/applecms/.iptv-result.m3u`；拉取失败不会破坏旧文件。示例中的主机、用户和路径必须替换，不会作为项目默认值。

首次 SSH 连接会记录主机指纹，请核对后接受。不要使用 `StrictHostKeyChecking=no`，也不要把私钥或密码提交到仓库。

## 局域网访问不到

1. 确保手机和播放器在同一个局域网。
2. 用 `ip addr` 查手机 WLAN 地址，不要使用 `127.0.0.1`。
3. 确保 Android 没有暂停 Termux。
4. 先在手机本机执行 `curl http://127.0.0.1:35455/allinone.m3u`。
5. 再从另一台设备访问 `http://手机IP:35455/allinone.m3u`。

IPv6 字面量会自动带方括号写进 M3U；只有手机、局域网和播放器都支持 IPv6 时才建议直接使用 IPv6 地址。
