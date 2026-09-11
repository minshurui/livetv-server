package main

// 解析器: 虎牙/斗鱼/抖音/YY → 真实签名流 URL (与 allinone.py 1:1)

import (
	"compress/gzip"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	huyaCache  = newTTLCache()
	douyuCache = newTTLCache()
	dyCache    = newTTLCache()
	yyCache    = newTTLCache()
)

func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func httpGetText(u, ua string, timeout int) (string, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", ua)
	client := newV4Client(time.Duration(timeout) * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	// 虎牙/抖音页面强制 content-encoding: gzip。client 因 DisableCompression=true
	// 不会自动解压，这里按响应头手动解压，否则正则拿不到明文会解析失败(503/404)。
	var body io.Reader = resp.Body
	if strings.EqualFold(resp.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(resp.Body)
		if err != nil {
			return "", err
		}
		defer gz.Close()
		body = gz
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ---------------- 虎牙 ----------------
// 页面含多条 sFlvUrl 线路(aldirect/tx/hs/al)。按稳定性优先级选:
// al > tx > hs > aldirect(最后兜底)。签名参数跨域通用。
// uid: 2026 新页面用 lPresenterUid(可能13位不截断); 兜底旧 "uid"
var (
	huyaLineRe         = regexp.MustCompile(`"sFlvUrl":"([^"]+)","sFlvUrlSuffix":"flv","sFlvAntiCode":"([^"]+)"`)
	huyaStreamRe       = regexp.MustCompile(`"sStreamName":"([^"]+)"`)
	huyaUidRe          = regexp.MustCompile(`"lPresenterUid":\s*"?(\d+)"?`)
	huyaUidOldRe       = regexp.MustCompile(`"uid":\s*"?(\d{5,13})"?`)
	huyaLuidRe         = regexp.MustCompile(`"lUid":\s*"?(\d{5,13})"?`)
	huyaStreamMarkerRe = regexp.MustCompile(`\bstream\s*:\s*`)
)

func huyaPref(u string) int {
	// 2026-09-03 实测: 仅 al 线返回 200, tx/hs 线对多数房间 403 → al 必须最优先。
	// al 单连接限流由 proxy.go 的 FLV 时间戳重写续流解决。
	switch {
	case strings.Contains(u, "al.flv.huya.com"):
		return 1
	case strings.Contains(u, "tx.flv.huya.com"):
		return 2
	case strings.Contains(u, "hs.flv.huya.com"):
		return 3
	case strings.Contains(u, "aldirect"):
		return 5
	}
	return 4
}

type huyaLine struct {
	base     string
	anti     string
	stream   string
	cdn      string
	uid      uint64
	priority int
}

type huyaStreamInfo struct {
	FlvURL       string          `json:"sFlvUrl"`
	FlvAntiCode  string          `json:"sFlvAntiCode"`
	StreamName   string          `json:"sStreamName"`
	CDN          string          `json:"sCdnType"`
	PresenterUID json.RawMessage `json:"lPresenterUid"`
	Priority     int             `json:"iWebPriorityRate"`
	WebPriority  int             `json:"iWebPriority"`
	MobileUID    json.RawMessage `json:"lUid"`
	ExtraUID     json.RawMessage `json:"uid"`
}

type huyaStreamPayload struct {
	Data []struct {
		GameStreamInfoList []huyaStreamInfo `json:"gameStreamInfoList"`
	} `json:"data"`
}

func rawUint64(raw json.RawMessage) uint64 {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	v, _ := strconv.ParseUint(s, 10, 64)
	return v
}

// findJSONValueEnd 找到从 start 开始的完整 JSON object/array，正确跳过字符串内括号。
func findJSONValueEnd(input string, start int) int {
	for start < len(input) && (input[start] == ' ' || input[start] == '\t' || input[start] == '\r' || input[start] == '\n') {
		start++
	}
	if start >= len(input) || (input[start] != '{' && input[start] != '[') {
		return -1
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(input); i++ {
		c := input[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

func unescapeJSONFragment(s string) string {
	if v, err := strconv.Unquote(`"` + s + `"`); err == nil {
		return v
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, `\/`, `/`), `\u0026`, `&`)
}

func extractHuyaLines(page string) []huyaLine {
	var lines []huyaLine
	if marker := huyaStreamMarkerRe.FindStringIndex(page); marker != nil {
		start := marker[1]
		if end := findJSONValueEnd(page, start); end > start {
			var payload huyaStreamPayload
			if err := json.Unmarshal([]byte(page[start:end]), &payload); err == nil {
				for _, data := range payload.Data {
					for _, info := range data.GameStreamInfoList {
						if info.FlvURL == "" || info.FlvAntiCode == "" || info.StreamName == "" {
							continue
						}
						uid := rawUint64(info.PresenterUID)
						if uid == 0 {
							uid = rawUint64(info.MobileUID)
						}
						if uid == 0 {
							uid = rawUint64(info.ExtraUID)
						}
						priority := info.Priority
						if priority == 0 {
							priority = info.WebPriority
						}
						lines = append(lines, huyaLine{
							base: info.FlvURL, anti: info.FlvAntiCode, stream: info.StreamName,
							cdn: info.CDN, uid: uid, priority: priority,
						})
					}
				}
			}
		}
	}

	if len(lines) > 0 {
		return lines
	}

	// 兼容旧页面：结构化 stream payload 不存在时再使用原来的相邻字段正则。
	ms := huyaLineRe.FindAllStringSubmatch(page, -1)
	stream := ""
	if sm := huyaStreamRe.FindStringSubmatch(page); len(sm) > 1 {
		stream = unescapeJSONFragment(sm[1])
	}
	uid := uint64(0)
	for _, re := range []*regexp.Regexp{huyaUidRe, huyaUidOldRe, huyaLuidRe} {
		if m := re.FindStringSubmatch(page); len(m) > 1 {
			uid, _ = strconv.ParseUint(m[1], 10, 64)
			break
		}
	}
	for _, m := range ms {
		lines = append(lines, huyaLine{
			base: unescapeJSONFragment(m[1]), anti: unescapeJSONFragment(m[2]),
			stream: stream, uid: uid,
		})
	}
	return lines
}

func rotateHuyaUID(uid uint64) uint64 {
	low := uid & 0xFFFFFFFF
	return ((((low << 8) | (low >> 24)) & 0xFFFFFFFF) | (uid & ^uint64(0xFFFFFFFF)))
}

func decodeHuyaFM(fm string) (string, error) {
	fm = strings.ReplaceAll(fm, " ", "+")
	raw, err := base64.StdEncoding.DecodeString(fm)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(fm, "="))
	}
	if err != nil {
		return "", err
	}
	prefix := string(raw)
	if i := strings.Index(prefix, "_"); i >= 0 {
		prefix = prefix[:i]
	}
	return prefix, nil
}

func buildHuyaAntiCode(stream, anti string, presenterUID uint64, now time.Time) (string, error) {
	anti = strings.ReplaceAll(anti, "&amp;", "&")
	query, err := url.ParseQuery(anti)
	if err != nil {
		return "", err
	}
	fm := query.Get("fm")
	wsTime := query.Get("wsTime")
	if fm == "" || wsTime == "" {
		return "", fmt.Errorf("missing fm/wsTime")
	}
	prefix, err := decodeHuyaFM(fm)
	if err != nil {
		return "", err
	}
	if presenterUID == 0 {
		presenterUID = 1400000000000 + uint64(now.UnixNano()%10000000)
	}
	ctype := query.Get("ctype")
	if ctype == "" {
		ctype = "huya_live"
	}
	platformID := query.Get("t")
	if platformID == "" {
		platformID = "100"
	}
	fs := query.Get("fs")
	if fs == "" {
		fs = "bgct"
	}
	if expiry, parseErr := strconv.ParseInt(wsTime, 16, 64); parseErr == nil && expiry < now.Unix()+20*60 {
		wsTime = strconv.FormatInt(now.Unix()+24*60*60, 16)
	}

	seqID := presenterUID + uint64(now.UnixMilli())
	secretHash := md5hex(fmt.Sprintf("%d|%s|%s", seqID, ctype, platformID))
	isWAP := platformID == "103"
	calcUID := rotateHuyaUID(presenterUID)
	if isWAP {
		calcUID = presenterUID
	}
	wsSecret := md5hex(fmt.Sprintf("%s_%d_%s_%s_%s", prefix, calcUID, stream, secretHash, wsTime))

	result := url.Values{}
	result.Set("wsSecret", wsSecret)
	result.Set("wsTime", wsTime)
	result.Set("seqid", strconv.FormatUint(seqID, 10))
	result.Set("ctype", ctype)
	result.Set("ver", "1")
	result.Set("fs", fs)
	result.Set("fm", fm)
	result.Set("t", platformID)
	if HUYA_CODEC != "" {
		result.Set("codec", HUYA_CODEC)
	}
	if isWAP {
		result.Set("uid", strconv.FormatUint(presenterUID, 10))
		result.Set("uuid", strconv.FormatUint(uint64(now.UnixMilli())%uint64(^uint32(0)), 10))
	} else {
		result.Set("u", strconv.FormatUint(calcUID, 10))
	}
	return result.Encode(), nil
}

func resolveHuya(rid string) string {
	key := "huya:" + rid
	if v, ok := huyaCache.get(key); ok {
		return v
	}
	page, err := httpGetText("https://www.huya.com/"+rid, UA_PC, TIMEOUT_S)
	if err != nil {
		logf("[huya %s] room page: %v", rid, err)
		huyaCache.set(key, "", 60)
		return ""
	}
	lines := extractHuyaLines(page)
	if len(lines) == 0 {
		logf("[huya %s] no FLV stream in page", rid)
		huyaCache.set(key, "", 60)
		return ""
	}
	sort.SliceStable(lines, func(i, j int) bool {
		iPreferred := HUYA_CDN != "" && strings.EqualFold(lines[i].cdn, HUYA_CDN)
		jPreferred := HUYA_CDN != "" && strings.EqualFold(lines[j].cdn, HUYA_CDN)
		if iPreferred != jPreferred {
			return iPreferred
		}
		pi, pj := huyaPref(lines[i].base), huyaPref(lines[j].base)
		if pi != pj {
			return pi < pj
		}
		return lines[i].priority > lines[j].priority
	})
	for _, line := range lines {
		if line.stream == "" {
			continue
		}
		anti, buildErr := buildHuyaAntiCode(line.stream, line.anti, line.uid, time.Now())
		if buildErr != nil {
			logf("[huya %s] build %s token: %v", rid, line.cdn, buildErr)
			continue
		}
		base := strings.Replace(line.base, "http://", "https://", 1)
		full := fmt.Sprintf("%s/%s.flv?%s", strings.TrimRight(base, "/"), line.stream, anti)
		huyaCache.set(key, full, 60)
		return full
	}
	huyaCache.set(key, "", 60)
	return ""
}

// ---------------- 斗鱼 ----------------
var (
	douyuRidRe  = regexp.MustCompile(`rid":(\d{1,8}),"vipId`)
	douyuShowRe = regexp.MustCompile(`"showTime"\s*:\s*(\d{10})`)
)

func pageShowAgeHours(page string) float64 {
	if page == "" {
		return -1
	}
	m := douyuShowRe.FindStringSubmatch(page)
	if len(m) < 2 {
		return -1
	}
	ts, _ := strconv.ParseInt(m[1], 10, 64)
	return float64(time.Now().Unix()-ts) / 3600.0
}

func resolveDouyu(rid string) string {
	key := "douyu:" + rid
	if v, ok := douyuCache.get(key); ok {
		return v
	}
	res := resolveDouyuInner(rid)
	douyuCache.set(key, res, 120)
	return res
}

func resolveDouyuInner(rid string) string {
	ua := UA_PC
	page := ""
	realRid := rid
	if p, err := httpGetText("https://m.douyu.com/"+rid, ua, TIMEOUT_S); err == nil {
		page = p
		if m := douyuRidRe.FindStringSubmatch(page); len(m) > 1 {
			realRid = m[1]
		}
	}
	apiURL := "https://playweb.douyucdn.cn/lapi/live/hlsH5Preview/" + realRid
	for attempt := 1; attempt <= 2; attempt++ {
		t13 := strconv.FormatInt(time.Now().UnixMilli(), 10)
		did := "10000000000000000000000000001501"
		if attempt == 2 {
			ua = UA_ANDROID
			did = md5hex(strconv.FormatFloat(float64(time.Now().UnixNano())/1e6, 'f', 0, 64))
		}
		auth := md5hex(realRid + t13)
		form := url.Values{}
		form.Set("rid", realRid)
		form.Set("did", did)
		req, err := http.NewRequest("POST", apiURL, strings.NewReader(form.Encode()))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", ua)
		req.Header.Set("rid", realRid)
		req.Header.Set("time", t13)
		req.Header.Set("auth", auth)
		client := newV4Client(time.Duration(TIMEOUT_S) * time.Second)
		resp, err := client.Do(req)
		if err != nil {
			dwLog(rid, fmt.Sprintf("api attempt%d err: %v", attempt, err))
			continue
		}
		var j map[string]interface{}
		dec := json.NewDecoder(resp.Body)
		decErr := dec.Decode(&j)
		resp.Body.Close()
		if decErr != nil {
			dwLog(rid, fmt.Sprintf("api attempt%d json err: %v", attempt, decErr))
			continue
		}
		errCode := intfToInt(j["error"])
		if errCode == 0 {
			d := mapString(j["data"])
			rtmpURL := strVal(d["rtmp_url"])
			rtmpLive := strVal(d["rtmp_live"])
			if rtmpURL != "" && rtmpLive != "" {
				full := rtmpURL + "/" + rtmpLive
				if strings.Contains(full, ".m3u8") {
					full = strings.Replace(full, ".m3u8", ".flv", 1)
				}
				return full
			}
			dwLog(rid, "error=0 但 rtmp 字段为空")
			return ""
		}
		if errCode == 102 || errCode == 103 || errCode == 104 || errCode == 2002 || errCode == 2005 {
			dwLog(rid, fmt.Sprintf("err %d (%v) → 无流, 判死", errCode, j["msg"]))
			return ""
		}
		if errCode == 742017 {
			ageH := pageShowAgeHours(page)
			if ageH >= 0 && ageH >= 24 {
				dwLog(rid, fmt.Sprintf("err 742017 → 无直播流(死房, showTime %.1fh 前)", ageH))
				return ""
			}
			ageStr := "未知"
			if ageH >= 0 {
				ageStr = fmt.Sprintf("%.1fh", ageH)
			}
			dwLog(rid, fmt.Sprintf("attempt%d err 742017 (疑似瞬断, showTime %s 前), 重试", attempt, ageStr))
			continue
		}
		dwLog(rid, fmt.Sprintf("attempt%d err %d (%v)", attempt, errCode, j["msg"]))
	}
	return ""
}

func dwLog(rid, msg string) {
	logf("[douyu %s] %s", rid, msg)
}

func intfToInt(v interface{}) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case json.Number:
		n, _ := t.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	case nil:
		return -999
	}
	return -999
}

func mapString(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return map[string]interface{}{}
}

func strVal(v interface{}) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ---------------- 抖音 ----------------
func resolveDouyin(rid string) string {
	key := "douyin:" + rid
	if v, ok := dyCache.get(key); ok {
		return v
	}
	u := fmt.Sprintf("https://webcast.amemv.com/webcast/room/reflow/info/?room_id=%s&app_id=1128&live_id=1", rid)
	page, err := httpGetText(u, UA_PC, TIMEOUT_S)
	res := ""
	if err == nil {
		var j map[string]interface{}
		if json.Unmarshal([]byte(page), &j) == nil {
			room := mapString(mapString(j["data"])["room"])
			stream := mapString(room["stream_url"])
			for _, k := range []string{"rtmp_pull_url", "flv_pull_url", "hls_pull_url"} {
				if v := strVal(stream[k]); v != "" {
					res = v
					break
				}
			}
		}
	}
	dyCache.set(key, res, 60)
	return res
}

// ---------------- YY (占位, 与 allinone.py 一致返回空) ----------------
func resolveYY(rid string) string {
	key := "yy:" + rid
	if v, ok := yyCache.get(key); ok {
		return v
	}
	yyCache.set(key, "", 60)
	return ""
}
