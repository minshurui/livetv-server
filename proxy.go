package main

// 19090: FLV 直通转发 (/stream/{platform}/{rid}) + HLS (ffmpeg)
// 对齐 stream-proxy.py v4:
//  - 解析失败/测试流 → 404 (房间未开播)
//  - FLV 魔数校验, 坏流 fresh 重试一次
//  - Referer 头(虎牙 CDN 防盗链 403 修复)
//  - 上游 HLS/非FLV → 404

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// httpClientV4: 强制 IPv4 + 禁 gzip 的共享客户端。
// ① 虎牙 CDN 同时有 v4/v6 记录, 手机 IPv6 链路不稳 → 周期性断流 1s(播放卡顿)。
//
//	与 Python stream-proxy 的 curl -4 对齐, 强制走 tcp4。
//
// ② Go http.Transport 默认自动发 Accept-Encoding: gzip 并透明解压,
//
//	虎牙 CDN 对带 gzip 头的请求直接断开/限流(实验: 无gzip=连续, 有gzip=0字节)。
//	DisableCompression: true = 不发送 Accept-Encoding, 与 curl 默认一致。
var httpClientV4 = &http.Client{
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
			return d.DialContext(ctx, "tcp4", addr)
		},
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 8 * time.Second,
		DisableCompression:    true,
	},
}

// 解析请求复用同一 Transport/TCP/TLS 连接。旧实现每换一个频道都新建 Transport，
// 必须重复 DNS、TCP 和 TLS 握手，是连续换台时最明显的固定延迟之一。
var resolverTransportV4 = &http.Transport{
	DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
		return d.DialContext(ctx, "tcp4", addr)
	},
	MaxIdleConns:          64,
	MaxIdleConnsPerHost:   16,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   8 * time.Second,
	ResponseHeaderTimeout: 8 * time.Second,
	DisableCompression:    true,
}

// newV4Client: 强制 IPv4 + 禁 gzip 的共享传输客户端(每次调用仍有独立总超时)。
func newV4Client(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: resolverTransportV4,
	}
}

// resolverFor: platform → resolve 函数
func resolverFor(platform string) func(rid string) string {
	switch platform {
	case "huya":
		return resolveHuya
	case "douyu":
		return resolveDouyu
	case "douyin":
		return resolveDouyin
	case "yy":
		return resolveYY
	}
	return nil
}

// isOfflineTestStream: 命中测试流/离线回退 → 房间不在播
func isOfflineTestStream(u string) bool {
	low := strings.ToLower(u)
	if strings.Contains(low, "jsdelivr") || strings.Contains(low, "testvideo") ||
		strings.Contains(low, "feiyang666999") || strings.HasSuffix(low, ".m3u8") ||
		strings.Contains(low, ".m3u8?") {
		return true
	}
	return false
}

func refererFor(platform, rid string) string {
	switch platform {
	case "huya":
		return "https://www.huya.com/" + rid
	case "douyu":
		return "https://www.douyu.com/" + rid
	}
	return ""
}

// fetchFirstBytes: 拉上游流 upTo 秒, 返回首块字节+状态+总字节(超时截断)
func fetchFirstBytes(rawURL string, secs int) ([]byte, int, int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs)*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, 0, 0, err
	}
	// 拉流请求 UA 与 Python stream-proxy 的 curl -A "Mozilla/5.0" 完全一致
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := httpClientV4.Do(req)
	if err != nil {
		return nil, 0, 0, err
	}
	defer resp.Body.Close()
	// 读首块 + 继续读满 secs
	br := bufio.NewReaderSize(resp.Body, 64*1024)
	first := make([]byte, 4096)
	n, _ := io.ReadFull(br, first)
	if n < 4096 {
		first = first[:n]
	}
	var total int64 = int64(n)
	buf := make([]byte, 32*1024)
	for {
		if ctx.Err() != nil {
			break
		}
		m, rerr := br.Read(buf)
		total += int64(m)
		if rerr != nil {
			break
		}
	}
	return first, resp.StatusCode, total, nil
}

// ---------------- HTTP Handler :19090 ----------------
type proxyHandler struct{}

func (h proxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.SplitN(r.URL.Path, "?", 2)[0]
	// HLS 模式
	if strings.HasPrefix(path, "/hls/") {
		handleHLS(w, r, path)
		return
	}
	// 兼容 /stream/ 前缀透传
	if strings.HasPrefix(path, "/stream/") {
		path = path[len("/stream"):]
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 {
		http.Error(w, "not proxied: "+path, 404)
		return
	}
	platform, rid := parts[0], parts[1]
	if platform != "huya" && platform != "douyu" && platform != "douyin" && platform != "yy" {
		http.Error(w, "not proxied: "+platform, 404)
		return
	}
	resolve := resolverFor(platform)
	if resolve == nil {
		http.Error(w, "resolve failed", 502)
		return
	}
	fresh := strings.Contains(r.URL.RawQuery, "fresh=1")

	realURL := ""
	if fresh {
		delCacheFor(platform, rid)
		realURL = resolve(rid)
	} else {
		realURL = resolve(rid)
	}
	// 解析为空 = 死房/无流 → 404 (与 Python stream-proxy 对 jsdelivr 测试流一致)
	if realURL == "" {
		logf("  -> /%s/%s offline (resolve empty) 404", platform, rid)
		http.Error(w, "offline (room not live)", 404)
		return
	}
	// 离线测试流 → 404
	if isOfflineTestStream(realURL) {
		logf("  -> /%s/%s offline (test fallback) 404", platform, rid)
		http.Error(w, "offline (room not live)", 404)
		return
	}

	// 拉流: 校验 FLV 首字节, 坏则 fresh 重试一次
	stream := openAndServe(w, platform, rid, realURL)
	if stream {
		return
	}
	// fresh 重试: 清缓存拿新签名 URL
	logf("  bad stream for /%s/%s, fresh retry", platform, rid)
	delCacheFor(platform, rid)
	realURL2 := resolve(rid)
	if realURL2 == "" || isOfflineTestStream(realURL2) {
		http.Error(w, "stream not FLV", 502)
		return
	}
	openAndServe(w, platform, rid, realURL2)
}

func delCacheFor(platform, rid string) {
	switch platform {
	case "huya":
		huyaCache.del("huya:" + rid)
	case "douyu":
		douyuCache.del("douyu:" + rid)
	case "douyin":
		dyCache.del("douyin:" + rid)
	case "yy":
		yyCache.del("yy:" + rid)
	}
}

// ---------------- FLV tag 时间戳重写续流 ----------------
// 虎牙 CDN 单连接限流(~1.7MB), 需断流后换新签名 URL 续拉。但不同连接的 FLV tag
// 时间戳基准不一致(实测相差可达 5s+), 直接字节拼接会导致播放器时间戳回退
// (画面卡顿/音频循环)。正确做法: 解析 FLV tag, 续流时把时间戳重写为单调递增。

// readBE24: FLV 大端 3 字节
func readBE24(b []byte) uint32 {
	return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
}

// readBE32: FLV 大端 4 字节
func readBE32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

// flvHeaderLen: 从首块解析 FLV header 总长(dataOffset + 4B PrevTagSize0), 默认 13。
func flvHeaderLen(first []byte, n int) int {
	if n >= 9 && string(first[:3]) == "FLV" {
		do := int(readBE32(first[5:9]))
		if do >= 9 && do <= 512 {
			return do + 4
		}
	}
	return 13
}

// writeBE24: 写 FLV 大端 3 字节
func writeBE24(b []byte, v uint32) {
	b[0] = byte(v >> 16)
	b[1] = byte(v >> 8)
	b[2] = byte(v)
}

const flvTagHeaderLen = 11 // 1 type + 3 datasize + 3 ts + 1 tsext + 3 streamid

// flvStreamWriter: 把 FLV tag 流写入 w, 可选地重写时间戳(续流无缝拼接用)。
// rewrite=true 时: 新连接首 tag 若比上一连接末尾(baseTS)小, 加偏移补到 baseTS+200ms,
// 之后所有 tag 时间戳单调递增 → 播放器完全无感。
// 返回: 客户端断开=true(写失败), 上游 EOF/错误=false。
type flvStreamWriter struct {
	br        io.Reader
	w         io.Writer
	flusher   http.Flusher
	baseTS    uint32 // 续流模式: 上一连接最后的 tag 时间戳
	rewrite   bool   // 是否重写时间戳(仅续流连接)
	offset    uint32 // 应用到所有 tag 的偏移(基于首 IDR 计算, 之后不改)
	offsetSet bool
	seenIDR   bool   // 续流连接: 是否已遇到第一个视频关键帧(IDR)
	lastVTS   uint32 // 已写出的视频最大时间戳(audio/video 各自单调, 互不挤占)
	lastATS   uint32 // 已写出的音频最大时间戳
	written   int64  // 写出的字节数
}

func newFLVStreamWriter(r io.Reader, w io.Writer, flusher http.Flusher, baseTS uint32, rewrite bool) *flvStreamWriter {
	return &flvStreamWriter{br: r, w: w, flusher: flusher, baseTS: baseTS, rewrite: rewrite}
}

// pump 循环转发, 直到 EOF 或客户端写失败。返回 true=客户端断开, false=上游结束。
// 只转发【完整 tag】: CDN 掐断可能断在 tag 中间, 残缺 tag 丢弃, 保证客户端
// 收到的始终是完整 tag 序列 → 续流拼接点在 tag 边界, FLV demuxer 不报错。
func (f *flvStreamWriter) pump() bool {
	hdr := make([]byte, flvTagHeaderLen)
	const maxTag = 2 << 20 // 单 tag 上限 2MB(正常直播 tag 远小于此)
	for {
		// 读 tag header(11B)
		if _, err := io.ReadFull(f.br, hdr); err != nil {
			return false // 上游 EOF/错误(连接被 CDN 掐断)
		}
		dataSize := int(readBE24(hdr[1:4]))
		ts := (uint32(hdr[7]) << 24) | readBE24(hdr[4:7]) // tsext<<24 | ts24

		// 读完整 tag(data + 4B prevTagSize)到缓冲 — 不足即残缺, 丢弃
		total := dataSize + 4
		if total > maxTag {
			return false // 异常超大 tag: 结构不可信
		}
		tag := make([]byte, total)
		if _, err := io.ReadFull(f.br, tag); err != nil {
			return false // 残缺 tag: 丢弃不写, 保证边界干净
		}

		if f.rewrite {
			// 续流连接: 等第一个视频 IDR(关键帧)才开始转发。
			// 虎牙 CDN 新连接从流中任意位置开始(常是非关键帧/音频帧), 若直接转发,
			// 播放器解码器无参考帧 → 花屏/卡顿, 且必须等下一个 IDR(最多一个 GOP,
			// 2s)才能恢复 → 播放器"反复播放同一片段"(用户实测 2~3s 循环)。
			// 丢弃 IDR 之前的所有 tag, 从 IDR 起转发 → 解码器立即出画, 无感续播。
			if !f.seenIDR {
				isIDR := hdr[0] == 9 && len(tag) >= 2 &&
					(tag[0]&0x0F) == 7 && tag[1] == 1 && // AVC NALU
					(tag[0]>>4) == 1 // frameType=1 关键帧
				if !isIDR {
					// 丢弃非 IDR tag(含 script/序列头/普通帧); 但音频序列头也无所谓,
					// 解码器需要等视频 IDR 重建画面。
					continue
				}
				f.seenIDR = true
				// 诊断: 记录续流后第一个 IDR 的原始时间戳
				logf("    [续流IDR] ts=%d size=%d (baseTS=%d)", ts, dataSize, f.baseTS)
				// 落入下方: 此 IDR 是第一个转发的 tag, 用它做时间戳基准
			} else {
				// 已过 IDR: 跳过后续的配置 tag(script/SPS/AAC seq), 避免解码器重置
				skip := false
				switch hdr[0] {
				case 18: // script tag
					skip = true
				case 9:
					if len(tag) >= 2 && tag[0]&0x0F == 7 && tag[1] == 0 {
						skip = true // AVC sequence header
					}
				case 8:
					if len(tag) >= 2 && tag[0]>>4 == 10 && tag[0]&0x0F == 0 {
						skip = true // AAC sequence header
					}
				}
				if skip {
					continue
				}
			}
			// offset 基于第一个转发的 tag(IDR)计算, 之后不再修改(避免累积膨胀:
			// audio/video 常共享时间戳, 若用全局 floor 不断推 offset, 每对 +1ms,
			// 长时间流音视频渐不同步 + 时间戳超前真实时钟 → 播放器异常)
			if !f.offsetSet {
				f.offsetSet = true
				if f.baseTS+60 > ts { // +60ms 防相等(比 +200 更平滑)
					f.offset = f.baseTS + 60 - ts
				} else {
					f.offset = 0
				}
				// 首 IDR 自身也须 > baseTS(否则首帧 DTS 与旧连接末尾相等)
				if f.baseTS != 0 && ts+f.offset <= f.baseTS {
					f.offset += f.baseTS + 1 - (ts + f.offset)
				}
			}
			ts += f.offset
			// 单调性: video 用视频自己的下界, audio 用音频自己的下界。
			// 不允许同类型 tag 时间戳相等/回退(ffmpeg muxer 报 "485 >= 485" 即此);
			// audio/video 之间允许相等(同一毫秒的 A/V 帧, 播放器完全接受)。
			if hdr[0] == 9 {
				if f.lastVTS != 0 && ts <= f.lastVTS {
					ts = f.lastVTS + 1
				}
				f.lastVTS = ts
			} else if hdr[0] == 8 {
				if f.lastATS != 0 && ts <= f.lastATS {
					ts = f.lastATS + 1
				}
				f.lastATS = ts
			}
			hdr[7] = byte(ts >> 24)
			writeBE24(hdr[4:7], ts&0xFFFFFF)
		}
		// 记录各类型最后时间戳(非 rewrite 模式也用, 供续流 baseTS 用)
		if hdr[0] == 9 {
			f.lastVTS = ts
		} else if hdr[0] == 8 {
			f.lastATS = ts
		}

		// 写 tag header + data + prevTagSize(原子写出, 播放器永远看到完整 tag)
		if _, werr := f.w.Write(hdr); werr != nil {
			return true // 客户端断开
		}
		f.written += flvTagHeaderLen
		if _, werr := f.w.Write(tag); werr != nil {
			return true // 客户端断开
		}
		f.written += int64(len(tag))
		if f.flusher != nil {
			f.flusher.Flush()
		}
	}
}

// openAndServe: 拉流转发 + 上游断流自动续流(FLV 时间戳重写保证无缝)。
// 虎牙 CDN 对单条连接限流(~0.5-2MB 后断开), 若直接断开, 播放器感知断流→自行
// 重连→产生 1s 黑屏卡顿。本函数在上游断开后自动清缓存换新签名 URL 重新连接,
// 解析 FLV tag 并重写时间戳为单调递增, 在同一客户端连接内无缝续写 → 播放器无感知。
func openAndServe(w http.ResponseWriter, platform, rid, rawURL string) bool {
	const maxReconn = 600 // 大上限: 直播无限时长, 正常终止靠客户端断开 / resolve empty / 连续失败
	const maxConsecFail = 3

	clientWrote := false
	var total int64
	reconn := 0
	consecFail := 0 // 连续续流失败(403/超时), 房间被 CDN 限流或下线时停止空转
	var lastTS uint32

	flusher, _ := w.(http.Flusher)

	// ---- 预解析: 当前连接存活期间后台准备下一个签名 URL ----
	// 虎牙 CDN 单连接限流 ~1.7MB 断开。续流若断流才同步 resolve(抓页面 300-800ms),
	// 播放器会感知 ~1s 数据缺口 → 缓冲耗尽 → 反复播放当前片段(用户实测)。
	// 预解析让下一个 URL 在断流前就绪, 断流即连 → 缺口 ≈ 0。
	var pfMu sync.Mutex
	var prefetched string
	var prefetching bool

	startPrefetch := func() {
		pfMu.Lock()
		if prefetching {
			pfMu.Unlock()
			return
		}
		prefetching = true
		pfMu.Unlock()
		go func() {
			delCacheFor(platform, rid)
			u := resolverFor(platform)(rid)
			pfMu.Lock()
			prefetched = u
			prefetching = false
			pfMu.Unlock()
		}()
	}
	takePrefetch := func() string {
		pfMu.Lock()
		defer pfMu.Unlock()
		u := prefetched
		prefetched = ""
		return u
	}

	for {
		if reconn > 0 {
			u := takePrefetch()
			if u == "" || isOfflineTestStream(u) {
				// 预解析未就绪(首连太短就断) → 同步 resolve 兜底
				delCacheFor(platform, rid)
				u = resolverFor(platform)(rid)
			}
			if u == "" || isOfflineTestStream(u) {
				logf("  -> /%s/%s reconnect#%d resolve empty, stop", platform, rid, reconn)
				break
			}
			rawURL = u
			logf("  -> /%s/%s reconnect#%d (%.1fMB so far)", platform, rid, reconn, float64(total)/1048576)
		}

		ctx, cancel := context.WithCancel(context.Background())
		req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
		if err != nil {
			cancel()
			break
		}
		// 拉流请求 UA 与 Python stream-proxy 的 curl -A "Mozilla/5.0" 完全一致
		req.Header.Set("User-Agent", "Mozilla/5.0")
		ref := refererFor(platform, rid)
		if ref != "" {
			req.Header.Set("Referer", ref)
		}
		resp, err := httpClientV4.Do(req)
		if err != nil {
			logf("  forward open fail /%s/%s: %v", platform, rid, err)
			cancel()
			if reconn > 0 {
				reconn++
				consecFail++
				if consecFail >= maxConsecFail {
					logf("  -> /%s/%s %d consec fails, stop", platform, rid, consecFail)
					break
				}
				time.Sleep(500 * time.Millisecond)
				continue
			}
			break
		}
		logf("  [dbg] %s/%s → %s (HTTP %d, CL=%s, TE=%s)", platform, rid, trimURL(rawURL), resp.StatusCode, resp.Header.Get("Content-Length"), resp.Header.Get("Transfer-Encoding"))
		if resp.StatusCode != 200 {
			logf("  forward status %d /%s/%s", resp.StatusCode, platform, rid)
			resp.Body.Close()
			cancel()
			if reconn > 0 {
				reconn++
				consecFail++
				if consecFail >= maxConsecFail {
					logf("  -> /%s/%s %d consec fails, stop", platform, rid, consecFail)
					break
				}
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return false
		}
		br := bufio.NewReaderSize(resp.Body, 64*1024)
		first := make([]byte, 4096)
		n, rerr := io.ReadFull(br, first)
		isFLV := n >= 3 && string(first[:3]) == "FLV"

		if !clientWrote {
			// 首连: 必须校验 FLV, 坏流返回 false(上层 fresh retry)
			if !isFLV || (rerr != nil && rerr != io.ErrUnexpectedEOF) {
				logf("  bad stream /%s/%s first=%q err=%v → fresh retry", platform, rid, first[:minInt(n, 16)], rerr)
				resp.Body.Close()
				cancel()
				return false
			}
			w.Header().Set("Content-Type", "video/x-flv")
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(200)
			clientWrote = true
		}

		// 连接存活中后台预解析下一个签名 URL(CDN 断流时无缝切换, 缺口≈0)
		startPrefetch()

		// 统一处理: 首连发送新 FLV header, 续流丢弃新 header(不重发)。
		head := flvHeaderLen(first, n)
		if reconn == 0 {
			// 首连: 发送 FLV header(dataOffset + PrevTagSize0)
			if head > n {
				head = n
			}
			w.Write(first[:head])
			total = int64(head)
		} else {
			if head > n {
				head = n
			}
		}
		if flusher != nil {
			flusher.Flush()
		}

		// 首块剩余(header 之后)+ br 合并为完整 tag 流(保证从 tag 边界开始)
		var src io.Reader = br
		if n > head {
			src = io.MultiReader(bytes.NewReader(first[head:n]), br)
		}
		sw := newFLVStreamWriter(src, w, flusher, lastTS, reconn > 0)
		clientClosed := sw.pump()
		// baseTS 锚点用视频最后时间戳(续流从 IDR 续, 保证画面时间连续)
		if sw.lastVTS != 0 {
			lastTS = sw.lastVTS
		} else if sw.lastATS != 0 {
			lastTS = sw.lastATS
		}
		total += sw.written
		if clientClosed {
			resp.Body.Close()
			cancel()
			logf("  -> /%s/%s %.1fMB client closed", platform, rid, float64(total)/1048576)
			return true
		}
		resp.Body.Close()
		cancel()

		if !clientWrote {
			return false
		}
		// 上游 EOF → 续流换新连接(CDN 单连接配额导致)
		if reconn < maxReconn {
			reconn++
			consecFail = 0
			pfMu.Lock()
			ready := prefetched != ""
			pfMu.Unlock()
			if !ready {
				// 无预解析 URL(极少见)才 sleep, 给网络平复时间
				time.Sleep(200 * time.Millisecond)
			}
			continue
		}
		logf("  -> /%s/%s %.1fMB done (%d reconn)", platform, rid, float64(total)/1048576, reconn)
		return true
	}
	return clientWrote
}

// ---------------- HLS (ffmpeg) ----------------
var (
	hlsMu       sync.Mutex
	hlsState    = map[string]*hlsProc{}
	hlsStarting = map[string]chan struct{}{}
)

type hlsProc struct {
	cmd     *exec.Cmd
	dir     string
	last    time.Time
	started time.Time
	done    chan struct{}
}

func hlsProcDone(st *hlsProc) bool {
	select {
	case <-st.done:
		return true
	default:
		return false
	}
}

func startFFmpeg(platform, rid string) (result *hlsProc) {
	key := platform + "_" + rid
	hlsMu.Lock()
	if st, ok := hlsState[key]; ok && !hlsProcDone(st) {
		hlsMu.Unlock()
		return st
	}
	delete(hlsState, key)
	if ready, ok := hlsStarting[key]; ok {
		hlsMu.Unlock()
		<-ready
		hlsMu.Lock()
		st := hlsState[key]
		hlsMu.Unlock()
		return st
	}
	ready := make(chan struct{})
	hlsStarting[key] = ready
	hlsMu.Unlock()
	defer func() {
		hlsMu.Lock()
		if result != nil {
			hlsState[key] = result
		}
		delete(hlsStarting, key)
		close(ready)
		hlsMu.Unlock()
	}()

	// 快速检查房间在线(避免死房间反复起 ffmpeg); 实际回源走本地 19090 续流层。
	delCacheFor(platform, rid)
	if u := resolverFor(platform)(rid); u == "" || isOfflineTestStream(u) {
		return nil
	}
	// 回源: 不直连虎牙 CDN(单连接限流会被掐断), 走本地 19090 续流层。
	// livetv 内部续流(换新签名 URL + FLV tag 时间戳重写)保证回源流不断,
	// ffmpeg 只需持续封装, 断流由续流层兜底。
	realURL := fmt.Sprintf("http://127.0.0.1:%s/stream/%s/%s", PROXY_PORT, platform, rid)
	d := filepath.Join(HLSRoot, key)
	_ = os.MkdirAll(d, 0755)
	// 清旧分片
	old, _ := os.ReadDir(d)
	for _, f := range old {
		_ = os.Remove(filepath.Join(d, f.Name()))
	}
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-user_agent", UA_PC,
		"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "5",
		"-i", realURL,
		"-map", "0:v:0", "-map", "0:a:0", "-c", "copy",
		"-f", "hls", "-hls_time", "2", "-hls_list_size", "20",
		"-hls_flags", "delete_segments",
		"-hls_segment_filename", filepath.Join(d, "seg_%05d.ts"),
		filepath.Join(d, "index.m3u8"))
	errFile, _ := os.OpenFile(filepath.Join(d, "ffmpeg.err"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	cmd.Stderr = errFile
	cmd.Stdout = nil
	if err := cmd.Start(); err != nil {
		if errFile != nil {
			_ = errFile.Close()
		}
		logf("[hls] ffmpeg start fail %s/%s: %v", platform, rid, err)
		return nil
	}
	st := &hlsProc{cmd: cmd, dir: d, last: time.Now(), started: time.Now(), done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		if errFile != nil {
			_ = errFile.Close()
		}
		close(st.done)
	}()
	logf("[hls] start %s pid=%d", key, cmd.Process.Pid)
	result = st
	return result
}

func handleHLS(w http.ResponseWriter, r *http.Request, path string) {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// /hls/{platform}/{rid}/{fname}
	if len(parts) < 4 || parts[0] != "hls" {
		http.Error(w, "bad hls path", 404)
		return
	}
	platform, rid, fname := parts[1], parts[2], parts[3]
	if platform != "huya" && platform != "douyu" {
		http.Error(w, "bad platform", 404)
		return
	}
	if !validHLSFilename(fname) {
		http.Error(w, "bad hls filename", 404)
		return
	}
	key := platform + "_" + rid
	hlsMu.Lock()
	st := hlsState[key]
	if st != nil {
		st.last = time.Now()
	}
	hlsMu.Unlock()
	if st == nil {
		st = startFFmpeg(platform, rid)
		if st == nil {
			http.Error(w, "hls start failed (room offline?)", 502)
			return
		}
	}
	fp := filepath.Join(st.dir, fname)
	// 冷启动等(最多 30s)直到文件出现或进程退出
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(fp); err == nil {
			break
		}
		if hlsProcDone(st) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// index.m3u8: 播放器要等首个分片就绪才真正能播, 否则连拉 seg_00000 404。
	// 这里等到至少一个分片出现(或进程退出)再返回 m3u8。
	if strings.HasSuffix(fname, ".m3u8") {
		for time.Now().Before(deadline) {
			if segs, _ := filepath.Glob(filepath.Join(st.dir, "seg_*.ts")); len(segs) > 0 {
				break
			}
			if hlsProcDone(st) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	data, err := os.ReadFile(fp)
	if err != nil {
		http.Error(w, "segment not ready", 404)
		return
	}
	if strings.HasSuffix(fname, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	} else {
		w.Header().Set("Content-Type", "video/mp2t")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func validHLSFilename(name string) bool {
	if name == "index.m3u8" {
		return true
	}
	if !strings.HasPrefix(name, "seg_") || !strings.HasSuffix(name, ".ts") {
		return false
	}
	n := strings.TrimSuffix(strings.TrimPrefix(name, "seg_"), ".ts")
	if n == "" {
		return false
	}
	for _, c := range n {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// hlsWatchdog: 空闲回收(300s) / 进程死或 stale(25s)重启
func hlsWatchdog(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			hlsMu.Lock()
			var toRestart []string
			var toReap []string
			for key, st := range hlsState {
				dead := hlsProcDone(st)
				stale := false
				m3u := filepath.Join(st.dir, "index.m3u8")
				if fi, err := os.Stat(m3u); err == nil {
					stale = now.Sub(fi.ModTime()) > 25*time.Second
				} else if now.Sub(st.started) > 45*time.Second {
					stale = true // 冷启动超时没产出
				}
				idle := now.Sub(st.last)
				if idle > 300*time.Second {
					toReap = append(toReap, key)
				} else if dead || stale {
					toRestart = append(toRestart, key)
				}
			}
			for _, k := range toReap {
				if st := hlsState[k]; st != nil {
					_ = st.cmd.Process.Kill()
					<-st.done
					delete(hlsState, k)
					_ = os.RemoveAll(st.dir)
					logf("[hls] idle reaped %s", k)
				}
			}
			for _, k := range toRestart {
				st := hlsState[k]
				_ = st.cmd.Process.Kill()
				<-st.done
				delete(hlsState, k)
				parts := strings.SplitN(k, "_", 2)
				logf("[hls] restart %s (dead/stale)", k)
				go startFFmpeg(parts[0], parts[1])
			}
			hlsMu.Unlock()
		}
	}
}

var _ = net.IPv4len
var _ = fmt.Sprintf
