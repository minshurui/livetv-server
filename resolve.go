package main

// 解析器: 虎牙/斗鱼/抖音/YY → 真实签名流 URL (与 allinone.py 1:1)

import (
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
	b, err := io.ReadAll(resp.Body)
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
	huyaLineRe   = regexp.MustCompile(`"sFlvUrl":"([^"]+)","sFlvUrlSuffix":"flv","sFlvAntiCode":"([^"]+)"`)
	huyaStreamRe = regexp.MustCompile(`"sStreamName":"([^"]+)"`)
	huyaUidRe    = regexp.MustCompile(`"lPresenterUid":\s*"?(\d+)"?`)
	huyaUidOldRe = regexp.MustCompile(`"uid":\s*"?(\d{5,12})"?`)
	huyaLuidRe   = regexp.MustCompile(`"lUid":(\d{5,12})`)
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
	base string
	anti string
}

func resolveHuya(rid string) string {
	key := "huya:" + rid
	if v, ok := huyaCache.get(key); ok {
		return v
	}
	page, err := httpGetText("https://www.huya.com/"+rid, UA_PC, TIMEOUT_S)
	if err != nil {
		huyaCache.set(key, "", 60)
		return ""
	}
	ms := huyaLineRe.FindAllStringSubmatch(page, -1)
	if len(ms) == 0 {
		huyaCache.set(key, "", 60)
		return ""
	}
	lines := make([]huyaLine, 0, len(ms))
	for _, m := range ms {
		lines = append(lines, huyaLine{base: m[1], anti: m[2]})
	}
	sort.SliceStable(lines, func(i, j int) bool {
		return huyaPref(lines[i].base) < huyaPref(lines[j].base)
	})
	flvBase, anti := lines[0].base, lines[0].anti

	sm := huyaStreamRe.FindStringSubmatch(page)
	if len(sm) < 2 {
		huyaCache.set(key, "", 60)
		return ""
	}
	stream := sm[1]

	antiDec := strings.ReplaceAll(anti, `\"`, `"`)
	params := map[string]string{}
	for _, p := range strings.Split(antiDec, "&") {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 {
			params[kv[0]] = kv[1]
		}
	}
	wsTime := params["wsTime"]
	fmDec, _ := url.QueryUnescape(params["fm"])
	raw, err := base64.StdEncoding.DecodeString(fmDec + "==")
	if err != nil {
		raw = []byte(fmDec)
	}
	fmPre := ""
	if i := strings.Index(string(raw), "_"); i >= 0 {
		fmPre = string(raw)[:i]
	} else {
		fmPre = string(raw)
	}

	// 主播 uid (lPresenterUid 优先)
	u := ""
	if m := huyaUidRe.FindStringSubmatch(page); len(m) > 1 {
		u = m[1]
	} else if m := huyaUidOldRe.FindStringSubmatch(page); len(m) > 1 {
		u = m[1]
	} else if m := huyaLuidRe.FindStringSubmatch(page); len(m) > 1 {
		u = m[1]
	}
	if u == "" {
		u = "0"
	}
	seqid := strconv.FormatInt(time.Now().UnixNano()/100, 10) // 约 now*1e7
	wsSecret := md5hex(strings.Join([]string{fmPre, u, stream, seqid, wsTime}, "_"))

	// 关键: URL 不带 fm 参数(带 fm 会被 CDN 限流断开) — 与 Python allinone.py 完全一致。
	// 2026-09-03: 之前 Go 版多带了 txyp/fs/sphdcdn 等参数 → 每条连接 ~1.7MB 必断。
	// Python 版只有 wsSecret&wsTime&u&seqid 4 个参数, 连接持续不断。
	full := fmt.Sprintf("%s/%s.flv?wsSecret=%s&wsTime=%s&u=%s&seqid=%s",
		flvBase, stream, wsSecret, wsTime, u, seqid)
	huyaCache.set(key, full, 60)
	return full
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
