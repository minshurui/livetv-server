package main

// livetv 入口: 35455(解析/线路m3u/聚合m3u) + 19090(FLV/HLS) + 健康检查循环

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // 内嵌时区表: Termux 无 tzdata 包时仍按本地时区显示
)

// newRand: 独立随机源(打散顺序/限速间隔)
func newRand() *rand.Rand {
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

// ---------------- 35455 handler (对齐 allinone.py do_GET) ----------------
type aioHandler struct{}

// reqHost: 从请求 Host 头提取"外部可达的主机名/IP"(去掉端口)。
// 换 WiFi 后手机 IP 会变, 若 m3u 还用启动时的 PUBLIC_HOST 快照就会失效;
// 改为跟随访问者的 Host —— 用户用哪个地址访问8081, m3u里就用哪个地址,
// 换网络后无需改任何配置。例如:
//
//	http://127.0.0.1:8081/allinone.m3u     → 流地址 http://127.0.0.1:19090/...
func reqHost(r *http.Request) string {
	h := strings.TrimSpace(r.Host)
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	} else {
		h = strings.Trim(h, "[]")
	}
	if h == "" || strings.ContainsAny(h, "/\\@ ") {
		return normalizeURLHost(PUBLIC_HOST)
	}
	return normalizeURLHost(h)
}

func normalizeURLHost(h string) string {
	h = strings.Trim(strings.TrimSpace(h), "[]")
	// URL 中的 IPv6 字面量必须带方括号；旧实现按第一个冒号切分会把 IPv6 截断。
	if ip := net.ParseIP(h); ip != nil && ip.To4() == nil {
		return "[" + h + "]"
	}
	return h
}

func (h *aioHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.SplitN(r.URL.Path, "?", 2)[0]
	host := reqHost(r)
	// 聚合 m3u (nginx 8081 反代到此处, 替代 live-m3u.php)
	if path == "/allinone.m3u" {
		// 触发健康检查(白名单旧)
		go maybeTriggerHealth()
		body := aggregateM3U(host)
		w.Header().Set("Content-Type", "audio/x-mpegurl")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		w.Write([]byte(body))
		return
	}
	// 线路 m3u
	if path == "/huyayqk.m3u" || path == "/douyuyqk.m3u" || path == "/yylunbo.m3u" {
		switch path {
		case "/huyayqk.m3u":
			serveBody(w, "audio/x-mpegurl", lineM3U("huya", host))
		case "/douyuyqk.m3u":
			serveBody(w, "audio/x-mpegurl", lineM3U("douyu", host))
		default: // yylunbo: 全测试流 → 空 m3u (与 NAS 一致)
			serveBody(w, "audio/x-mpegurl", "#EXTM3U\n")
		}
		return
	}
	// /{platform}/{rid} → 301
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		http.Error(w, "not supported: "+path, 404)
		return
	}
	platform, rid := parts[0], parts[1]
	resolve := resolverFor(platform)
	if resolve == nil {
		http.Error(w, "not supported: "+path, 404)
		return
	}
	fresh := strings.Contains(r.URL.RawQuery, "fresh=1")
	target := ""
	if fresh {
		delCacheFor(platform, rid)
		target = resolve(rid)
	} else {
		target = resolve(rid)
	}
	if target == "" {
		// 旧实现跳到测试录像，播放器会把离线房间误认为“可播直播”。
		// 保持路径接口不变，但用明确的 404 表示未开播/解析失败。
		http.Error(w, "offline (room not live)", http.StatusNotFound)
		return
	}
	w.Header().Set("Location", target)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(301)
}

func serveBody(w http.ResponseWriter, ct, body string) {
	w.Header().Set("Content-Type", ct)
	w.Write([]byte(body))
}

// ---------------- main ----------------
func main() {
	// 时区: 不依赖环境变量/系统 zoneinfo, 内嵌 tzdata 强制 Asia/Shanghai
	if os.Getenv("TZ") == "" {
		os.Setenv("TZ", "Asia/Shanghai")
	}
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		time.Local = loc
	}
	logf("=== livetv 启动 (Go 版直播服务) ===")
	logf("PUBLIC_HOST=%s AIO_PORT=%s PROXY_PORT=%s", PUBLIC_HOST, AIO_PORT, PROXY_PORT)

	_ = os.MkdirAll(LogDir, 0755)
	_ = os.MkdirAll(HLSRoot, 0755)
	_ = os.MkdirAll(AppleCMS, 0755)
	_ = os.MkdirAll(filepath.Dir(ChannelsF), 0755)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// HLS watchdog
	go hlsWatchdog(ctx)

	// 启动时健康检查(后台)
	go runHealthCheck("startup")

	// 周期健康检查
	go func() {
		t := time.NewTicker(time.Duration(HealthIntervalS) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				go runHealthCheck("timer")
			}
		}
	}()

	// 35455
	aioSrv := newHTTPServer(AIO_PORT, &aioHandler{})
	// 19090
	prxSrv := newHTTPServer(PROXY_PORT, proxyHandler{})

	errCh := make(chan error, 2)
	go func() { errCh <- aioSrv.ListenAndServe() }()
	go func() { errCh <- prxSrv.ListenAndServe() }()
	logf("监听 :%s (解析/线路m3u/聚合m3u) + :%s (FLV/HLS)", AIO_PORT, PROXY_PORT)

	// 端口占用快速诊断
	time.Sleep(300 * time.Millisecond)
	for _, p := range []string{AIO_PORT, PROXY_PORT} {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+p, 200*time.Millisecond)
		if err != nil {
			logf("!! 端口 %s 未监听: %v", p, err)
		} else {
			c.Close()
		}
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	select {
	case s := <-sig:
		logf("收到信号 %v, 退出", s)
		cancel()
		aioSrv.Close()
		prxSrv.Close()
	case e := <-errCh:
		logf("服务错误退出: %v", e)
		cancel()
	}
}

func newHTTPServer(port string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
}

var _ = fmt.Sprintf
