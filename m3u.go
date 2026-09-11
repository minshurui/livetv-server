package main

// m3u 生成: 线路 m3u(/huyayqk.m3u /douyuyqk.m3u) + 聚合 m3u(/allinone.m3u)
// 对齐: allinone.py serve_line_m3u + live-m3u.php

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	prewarmMu      sync.Mutex
	prewarmRunning = map[string]bool{}
	prewarmLast    = map[string]time.Time{}
)

func extinfGroup(inf string) string {
	const marker = `group-title="`
	start := strings.Index(inf, marker)
	if start < 0 {
		return ""
	}
	rest := inf[start+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func prewarmCandidates(entries [][2]string, perGroup, maxChannels int) []string {
	if perGroup <= 0 || maxChannels <= 0 {
		return nil
	}
	counts := map[string]int{}
	result := make([]string, 0, maxChannels)
	for _, entry := range entries {
		rid, group := entry[0], extinfGroup(entry[1])
		if rid == "" || counts[group] >= perGroup {
			continue
		}
		counts[group]++
		result = append(result, rid)
		if len(result) >= maxChannels {
			break
		}
	}
	return result
}

func filterChannelEntries(entries [][2]string, allowed map[string]bool) [][2]string {
	if allowed == nil {
		return entries
	}
	result := make([][2]string, 0, len(entries))
	for _, entry := range entries {
		if allowed[entry[0]] {
			result = append(result, entry)
		}
	}
	return result
}

// maybePrewarmPlatform 在播放器加载列表后，后台预解析每组前几个频道。
// 只解析签名、不拉视频流；五分钟内不重复，避免大列表刷新造成平台压力。
func maybePrewarmPlatform(platform string, entries [][2]string) {
	candidates := prewarmCandidates(entries, PREWARM_PER_GROUP, PREWARM_MAX_CHANNELS)
	if len(candidates) == 0 {
		return
	}
	prewarmMu.Lock()
	if prewarmRunning[platform] || time.Since(prewarmLast[platform]) < 5*time.Minute {
		prewarmMu.Unlock()
		return
	}
	prewarmRunning[platform] = true
	prewarmLast[platform] = time.Now()
	prewarmMu.Unlock()

	go func() {
		defer func() {
			prewarmMu.Lock()
			prewarmRunning[platform] = false
			prewarmMu.Unlock()
		}()
		resolve := resolverFor(platform)
		if resolve == nil {
			return
		}
		jobs := make(chan string)
		var workers sync.WaitGroup
		for i := 0; i < PREWARM_WORKERS; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for rid := range jobs {
					resolve(rid)
				}
			}()
		}
		for _, rid := range candidates {
			jobs <- rid
		}
		close(jobs)
		workers.Wait()
		logf("[prewarm] %s 预解析完成: %d 个频道", platform, len(candidates))
	}()
}

// channels.json 结构: {"huya": [[rid, "#EXTINF...组名..., 频道名"], ...], "douyu": [...]}
type channelsFile map[string][][2]string

func loadChannels() channelsFile {
	b, err := os.ReadFile(ChannelsF)
	if err != nil {
		logf("[m3u] 读 channels.json 失败: %v", err)
		return nil
	}
	var ch channelsFile
	if json.Unmarshal(b, &ch) != nil {
		logf("[m3u] channels.json 解析失败")
		return nil
	}
	return ch
}

// aliveWhitelist: 白名单新鲜(<ALIVE_MAXAGE)才返回 set; 过期/缺失返回 nil(=全量保底)
func aliveWhitelist() map[string]bool {
	stampB, err1 := os.ReadFile(StampF)
	aliveB, err2 := os.ReadFile(AliveF)
	if err1 != nil || err2 != nil {
		return nil
	}
	ts, err := parseFloat(strings.TrimSpace(string(stampB)))
	if err != nil || time.Now().Unix()-int64(ts) >= ALIVE_MAXAGE {
		return nil
	}
	set := map[string]bool{}
	for _, r := range strings.Fields(string(aliveB)) {
		set[r] = true
	}
	return set
}

func aliveAgeSec() int64 {
	b, err := os.ReadFile(StampF)
	if err != nil {
		return -1
	}
	ts, err := parseFloat(strings.TrimSpace(string(b)))
	if err != nil {
		return -1
	}
	return time.Now().Unix() - int64(ts)
}

func addGroupPrefix(inf, prefix string) string {
	if strings.Contains(inf, `group-title="`) && !strings.Contains(inf, prefix+"·") {
		inf = strings.Replace(inf, `group-title="`, `group-title="`+prefix+"·", 1)
	}
	return strings.TrimRight(strings.TrimRight(inf, "\ufffd"), " \r\n")
}

func localizeChannelLogo(inf, platform, rid, host string) string {
	const marker = `tvg-logo="`
	start := strings.Index(inf, marker)
	if start < 0 || rid == "" || host == "" {
		return inf
	}
	valueStart := start + len(marker)
	relEnd := strings.IndexByte(inf[valueStart:], '"')
	if relEnd < 0 {
		return inf
	}
	valueEnd := valueStart + relEnd
	if inf[valueStart:valueEnd] == "" || strings.Contains(inf[valueStart:valueEnd], "/logo/") {
		return inf
	}
	// URL 只含平台和房间号，头像源变化也不改变客户端缓存键；真实源从 channels.json 查找。
	local := "http://" + host + ":" + PUBLIC_LIVETV_PORT + "/logo/" + platform + "/" + rid
	return inf[:valueStart] + local + inf[valueEnd:]
}

// lineM3U: /huyayqk.m3u /douyuyqk.m3u — URL = http://host:AIO_PORT/{platform}/{rid}
// host = 访问者 Host(动态适配当前网络), 回退 PUBLIC_HOST
// douyu: 白名单过滤(与 Python serve_line_m3u 一致)
func lineM3U(platform, host string) string {
	if host == "" {
		host = PUBLIC_HOST
	}
	ch := loadChannels()
	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")
	prefix := "虎牙"
	if platform != "huya" {
		prefix = "斗鱼"
	}
	alive := map[string]bool(nil)
	if platform == "douyu" {
		alive = aliveWhitelist()
	}
	entries := ch[platform]
	maybePrewarmPlatform(platform, filterChannelEntries(entries, alive))
	for _, e := range entries {
		rid, inf := e[0], e[1]
		if alive != nil && !alive[rid] {
			continue
		}
		inf = localizeChannelLogo(inf, platform, rid, host)
		sb.WriteString(addGroupPrefix(inf, prefix) + "\n")
		if platform == "huya" {
			// 默认走 Go 续流层：上游 CDN 断开时保持客户端连接并修正 FLV 时间戳。
			// 19091 Python 旧代理继续保留，便于已有外部链接回滚。
			sb.WriteString("http://" + host + ":" + PUBLIC_PROXY_PORT + "/stream/huya/" + rid + "\n")
		} else {
			sb.WriteString("http://" + host + ":" + PUBLIC_AIO_PORT + "/" + platform + "/" + rid + "\n")
		}
	}
	return sb.String()
}

// aggregateM3U: /allinone.m3u (nginx 8081 入口)
// 虎牙全量(19090直通) + 斗鱼白名单(19090转发) + 电视快照
func aggregateM3U(host string) string {
	if host == "" {
		host = PUBLIC_HOST
	}
	var sb strings.Builder
	sb.WriteString("#EXTM3U\n")
	age := aliveAgeSec()
	set := aliveWhitelist()
	n := len(set)
	ageStr := "无"
	if age >= 0 {
		ageStr = sprintf("%ds", age)
	}
	sb.WriteString("# livetv 实时聚合 " + nowStr() + " | 斗鱼白名单 " + itoa(n) + " rid (age " + ageStr + ")\n")

	ch := loadChannels()
	maybePrewarmPlatform("huya", ch["huya"])
	maybePrewarmPlatform("douyu", filterChannelEntries(ch["douyu"], set))
	// 虎牙: 全量, 19090 Go FLV 续流层处理 CDN 断流和时间戳回退。
	for _, e := range ch["huya"] {
		rid, inf := e[0], e[1]
		if rid == "" {
			continue
		}
		inf = localizeChannelLogo(inf, "huya", rid, host)
		sb.WriteString(addGroupPrefix(inf, "虎牙") + "\n")
		sb.WriteString("http://" + host + ":" + PUBLIC_PROXY_PORT + "/stream/huya/" + rid + "\n")
	}
	// 斗鱼: 白名单过滤, 19090 转发
	for _, e := range ch["douyu"] {
		rid, inf := e[0], e[1]
		if rid == "" {
			continue
		}
		if set != nil && !set[rid] {
			continue
		}
		inf = localizeChannelLogo(inf, "douyu", rid, host)
		sb.WriteString(addGroupPrefix(inf, "斗鱼") + "\n")
		sb.WriteString("http://" + host + ":" + PUBLIC_PROXY_PORT + "/stream/douyu/" + rid + "\n")
	}
	// 电视快照 (去 #EXTM3U 头)
	if tv, err := os.ReadFile(TvF); err == nil {
		tvS := string(tv)
		tvS = trimExtm3uHeader(tvS)
		sb.WriteString(tvS)
		if !strings.HasSuffix(tvS, "\n") {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

func trimExtm3uHeader(s string) string {
	s = strings.TrimPrefix(s, "#EXTM3U")
	s = strings.TrimLeft(s, "\r\n \t\ufeff")
	return s
}
