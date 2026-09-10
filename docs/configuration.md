# 配置参考

| 变量 | 默认值 | 用途 |
|---|---:|---|
| `DATA` | Termux `$HOME`；容器 `/data` | 数据根目录 |
| `PUBLIC_HOST` | `127.0.0.1` | Host 无法从请求推断时的回退公开地址 |
| `AIO_PORT` | `35455` | M3U/解析服务 |
| `PROXY_PORT` | `19090` | 斗鱼代理 |
| `PY_PORT` | `19091` | 虎牙 FLV 代理 |
| `RES_PORT` | `35456` | 虎牙解析服务 |
| `TZ` | `Asia/Shanghai` | 容器时区 |
| `LIVETV_DATA_DIR` | `../data` | Compose 宿主机持久化目录 |
| `NAS_HOST` / `NAS_USER` | 无 | 可选 NAS 同步脚本连接信息 |

将部署变量保存在 `docker/.env`（由 `docker/.env.example` 复制）或编排平台的密钥管理中。`.env` 已被 Git 忽略。
