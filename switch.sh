#!/bin/bash
# 切换: Python 三件套(allinone/stream-proxy) → Go 单文件 livetv
# 用法: bash switch.sh [start|stop|status|restart]
HOME_DIR="/data/data/com.termux/files/home"
LNMP="$HOME_DIR/lnmp"
LOG="$LNMP/logs"
cd "$LNMP/livetv" || exit 1

port_open() { curl -s -m 1 -o /dev/null "http://127.0.0.1:$1/" 2>/dev/null && return 0 || return 1; }

case "$1" in
  start)
    # 1. 停 Python 服务(若有)
    for pat in "allinone.py" "stream-proxy.py"; do
      for P in $(pgrep -f "$pat" 2>/dev/null); do
        [ "$P" != "$$" ] && [ "$P" != "$PPID" ] && kill "$P" 2>/dev/null && echo "  停 Python: $pat ($P)"
      done
    done
    sleep 2
    # 2. 启 Go livetv
    if port_open 35455 && port_open 19090; then
      echo "  [skip] livetv 已在运行"
    else
      setsid nohup ./livetv > "$LOG/livetv.log" 2>&1 &
      echo "  启动 livetv PID $!"
      sleep 2
    fi
    # 3. 状态
    for P in 35455 19090; do
      if port_open $P; then echo "  ✅ :$P 在线"; else echo "  ❌ :$P 未监听"; fi
    done
    pgrep -f "livetv" > /dev/null && echo "  ✅ livetv 进程 $(pgrep -f livetv | head -1)"
    ;;
  stop)
    for P in $(pgrep -f "livetv" 2>/dev/null); do
      [ "$P" != "$$" ] && [ "$P" != "$PPID" ] && kill "$P" 2>/dev/null && echo "  停 livetv ($P)"
    done
    echo "  注意: 停掉后直播服务不可用, 恢复请跑 start (Python 版已停)"
    ;;
  status)
    for P in 35455 19090; do
      if port_open $P; then echo "  ✅ :$P 在线"; else echo "  ❌ :$P 未监听"; fi
    done
    pgrep -f livetv >/dev/null && echo "  ✅ livetv 进程: $(pgrep -f livetv | head -1)" || echo "  ❌ livetv 未运行"
    ;;
  restart)
    bash "$0" stop; sleep 2; bash "$0" start
    ;;
  *)
    echo "用法: $0 [start|stop|status|restart]"
    ;;
esac
