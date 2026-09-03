package main

// livetv — 直播全链路单二进制 (替代 allinone.py + stream-proxy.py + live-m3u.php + douyu-health.sh)
// 端口:  35455 = 解析器/线路m3u/聚合m3u ; 19090 = FLV直通 + HLS
// 环境变量: PUBLIC_HOST(默认192.168.2.2) AIO_PORT(35455) PROXY_PORT(19090) DATA(数据根目录, 容器用)

import (
	"os"
	"path/filepath"
)

// 数据根目录: 手机默认 /data/data/com.termux/files/home, Docker 容器用 DATA=/data 覆盖。
// 全部读写路径派生自 dataRoot, 保证同一份二进制手机/容器通用。
var (
	dataRoot  = envOr("DATA", "/data/data/com.termux/files/home")
	LNMP      = dataRoot + "/lnmp"
	ChannelsF = LNMP + "/allinone/channels.json"
	AppleCMS  = LNMP + "/applecms"
	AliveF    = AppleCMS + "/douyu-alive.txt"
	StampF    = AppleCMS + "/.douyu-health.stamp"
	TvF       = AppleCMS + "/.iptv-result.m3u"
	LogDir    = LNMP + "/logs"
	HLSRoot   = LNMP + "/allinone/hls"
)

var (
	PUBLIC_HOST = envOr("PUBLIC_HOST", "192.168.2.2")
	AIO_PORT    = envOr("AIO_PORT", "35455")
	PROXY_PORT  = envOr("PROXY_PORT", "19090")
	PY_PORT     = envOr("PY_PORT", "19091") // 虎牙: Python stream-proxy (FLV 直通)
	PUBLIC_AIO  = "http://" + PUBLIC_HOST + ":" + AIO_PORT
	PUBLIC_PRX  = "http://" + PUBLIC_HOST + ":" + PROXY_PORT
)

const (
	UA_PC      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/95.0.4638.69 Safari/537.36"
	UA_ANDROID = "Mozilla/5.0 (Linux; Android 5.0; SM-G900P Build/LRX21T) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/75.0.3770.100 Mobile Safari/537.36"
	TEST_STREAM = "https://cdn.jsdelivr.net/gh/feiyang666999/testvideo/sdr1080pvideo/playlist.m3u8"
	TIMEOUT_S = 15
	FAIL_TTL  = 15  // 解析失败缓存秒数
	ALIVE_MAXAGE = 3600 // 白名单新鲜窗口 (live-m3u.php 同款 3600)
	ALIVE_TRIGGER = 300 // 超过此秒数 → m3u 拉取触发后台健康检查
	HealthIntervalS = 1800 // 全量健康检查周期(30min)
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func logf(format string, a ...interface{}) {
	f, err := os.OpenFile(LogDir+"/livetv.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err == nil {
		defer f.Close()
		ts := nowStr()
		msg := sprintf(format, a...)
		f.WriteString(ts + " " + msg + "\n")
	}
	// 同时 stderr
	stderrPrintf(nowStr()+" "+format+"\n", a...)
}

var _ = filepath.Join
