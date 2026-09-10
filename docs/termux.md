# Termux 部署

在 Termux 中编译并运行 Go 服务前，安装 Go、Python 与所需运行依赖。默认数据根目录为 `$HOME`，数据路径为 `$HOME/lnmp`。

```bash
go build -o livetv .
DATA="$HOME" PUBLIC_HOST="" ./livetv
```

Python 辅助脚本位于 `docker/`。如需从 NAS 同步电视快照，必须显式设置连接信息：

```bash
NAS_HOST=nas.example.internal NAS_USER=your-user \
  bash docker/scripts/sync_iptv_from_nas.sh
```

首次连接会保存 SSH 主机指纹；请核对指纹，不要为了省事关闭主机密钥校验。
