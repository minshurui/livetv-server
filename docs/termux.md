# Android Termux 部署手册

Termux 适合把闲置 Android 手机变成局域网直播服务。它运行 Go 聚合/斗鱼代理和 Python 虎牙代理，但不包含 Docker 镜像里的完整 `iptv-api` 电视源测速栈。

如果你需要自动收集、测速和更新大量 IPTV，建议把完整版部署在 NAS；Termux 可以只提供虎牙/斗鱼，或从 NAS 同步已经生成的 IPTV 快照。

## 能得到什么

默认播放地址：

```text
http://手机局域网IP:35455/allinone.m3u
```

Termux 没有 Docker 中的 8081 聚合 nginx，所以入口是 35455。虎牙和斗鱼还会使用 19091、19090。

## 第一步：安装正确版本

建议从 F-Droid 或 Termux 官方 GitHub Releases 安装新版 Termux，不建议使用长期未更新的 Play 商店旧版。

手机系统设置中：

1. 允许 Termux 后台运行；
2. 关闭针对 Termux 的电池优化；
3. 允许自启动（系统提供时）；
4. 让手机和播放器连接同一个局域网。

## 第二步：安装依赖

```bash
pkg update
pkg upgrade
pkg install git golang python curl ffmpeg tmux openssh
termux-wake-lock
```

如果 `pkg update` 失败，先执行 `termux-change-repo` 更换可用镜像源。

## 第三步：下载和编译

```bash
cd "$HOME"
git clone https://github.com/minshurui/livetv-server.git
cd "$HOME/livetv-server"
go test ./...
go build -trimpath -ldflags '-s -w' -o livetv .
```

编译成功后应存在：

```bash
ls -lh "$HOME/livetv-server/livetv"
```

脚本从项目自身目录寻找程序，不要求使用固定的私人路径。

## 第四步：启动

```bash
cd "$HOME/livetv-server"
DATA="$HOME" bash switch.sh start
DATA="$HOME" bash switch.sh status
```

脚本会：

1. 创建 `$HOME/lnmp/` 数据目录；
2. 启动 Go 服务和虎牙 Python 代理；
3. 同步一次虎牙/斗鱼当前开播目录；
4. 把 PID 和日志保存到 `$HOME/lnmp/`。

停止和重启：

```bash
DATA="$HOME" bash switch.sh stop
DATA="$HOME" bash switch.sh restart
```

## 第五步：先在手机本机验证

```bash
curl -fsS http://127.0.0.1:35455/allinone.m3u | sed -n '1,10p'
tail -n 80 "$HOME/lnmp/logs/livetv.log"
tail -n 80 "$HOME/lnmp/logs/sync-channels.log"
```

第一行应为 `#EXTM3U`。首次目录同步受网络影响，暂时没有频道时先查看日志，不要不断重复启动。

## 第六步：找手机局域网 IP

```bash
ip addr show wlan0
```

找到类似 `192.168.x.x` 的地址，然后在另一台设备打开：

```text
http://192.168.x.x:35455/allinone.m3u
```

不要把 `127.0.0.1` 填给电视；它在电视上代表电视自己。

若使用 IPv6：

```text
http://[手机IPv6地址]:35455/allinone.m3u
```

手机、路由器和播放器必须都能直连该 IPv6 地址。

## 数据和日志位置

```text
$HOME/lnmp/
├── run/                         # PID 文件
├── logs/                        # 日志
├── allinone/channels.json       # 当前直播房间
└── applecms/.iptv-result.m3u    # 可选的 IPTV 快照
```

实时日志：

```bash
tail -f "$HOME/lnmp/logs/livetv.log"
tail -f "$HOME/lnmp/logs/huya-proxy.log"
tail -f "$HOME/lnmp/logs/sync-channels.log"
```

按 `Ctrl+C` 只结束日志查看。

## 自动刷新虎牙/斗鱼目录

`switch.sh start` 启动时同步一次。长期运行建议安装 `cronie`：

```bash
pkg install cronie
crond
crontab -e
```

加入：

```cron
17 */6 * * * DATA="$HOME" python3 "$HOME/livetv-server/docker/sync_channels.py" --out "$HOME/lnmp/allinone/channels.json" >> "$HOME/lnmp/logs/sync-channels.log" 2>&1
```

保存后检查：

```bash
crontab -l
pgrep -a crond
```

## Termux:Boot 自启动

安装 Termux:Boot，至少手动打开一次，然后：

```bash
mkdir -p "$HOME/.termux/boot"
nano "$HOME/.termux/boot/livetv.sh"
```

内容：

```bash
#!/data/data/com.termux/files/usr/bin/bash
termux-wake-lock
DATA="$HOME" bash "$HOME/livetv-server/switch.sh" start
crond
```

赋权：

```bash
chmod +x "$HOME/.termux/boot/livetv.sh"
```

手机重启后，如果服务没有起来，先检查 Android 是否禁止 Termux/Termux:Boot 自启动，而不是只检查脚本。

## 从 NAS 同步 IPTV 快照

先生成 SSH 密钥并把公钥加入 NAS：

```bash
ssh-keygen -t ed25519
ssh your-user@nas.example.internal
```

确认无密码登录和主机指纹正确后：

```bash
NAS_HOST=nas.example.internal \
NAS_USER=your-user \
NAS_M3U=/path/to/iptv-api/output/result.m3u \
DATA="$HOME" \
bash "$HOME/livetv-server/docker/scripts/sync_iptv_from_nas.sh"
```

脚本会验证频道数量后原子替换：

```text
$HOME/lnmp/applecms/.iptv-result.m3u
```

下载失败不会清空旧文件。示例主机、用户名和路径必须替换；不要把 SSH 私钥或密码写入仓库。

## 更新项目

```bash
cd "$HOME/livetv-server"
DATA="$HOME" bash switch.sh stop
git pull --ff-only
go test ./...
go build -trimpath -ldflags '-s -w' -o livetv .
DATA="$HOME" bash switch.sh start
```

数据位于 `$HOME/lnmp`，更新源码不会删除。

## 常见问题

### 本机能访问，电视不能访问

- 电视和手机必须网络互通；
- 地址必须使用手机 WLAN IP；
- Android 不能暂停 Termux；
- 路由器访客网络可能禁止设备互访；
- 19090、19091 也必须可达，否则列表能打开但平台频道无法播放。

### 服务过一会儿消失

通常是 Android 后台限制。重新执行 `termux-wake-lock`，关闭电池优化，并允许自启动。普通 shell 后台进程无法保证绕过厂商系统的强制清理。

### 离线房间播放成其他视频

最新版本不会使用测试录像回退。离线或解析失败应返回：

```text
404 offline (room not live)
```

出现旧行为时重新拉取源码并编译。
