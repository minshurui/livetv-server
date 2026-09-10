package main

// m3u 生成: 线路 m3u(/huyayqk.m3u /douyuyqk.m3u) + 聚合 m3u(/allinone.m3u)
// 对齐: allinone.py serve_line_m3u + live-m3u.php

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

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
	for _, e := range entries {
		rid, inf := e[0], e[1]
		if alive != nil && !alive[rid] {
			continue
		}
		sb.WriteString(addGroupPrefix(inf, prefix) + "\n")
		if platform == "huya" {
			// 虎牙: 走 Python stream-proxy(19091) FLV 直通, 断流1s自行续播(不折腾续流层)
			sb.WriteString("http://" + host + ":" + PUBLIC_PY_PORT + "/stream/huya/" + rid + "\n")
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
	// 虎牙: 全量, 19090 FLV 直通(续流层处理 CDN 限流, 播放器无感知)
	for _, e := range ch["huya"] {
		rid, inf := e[0], e[1]
		if rid == "" {
			continue
		}
		sb.WriteString(addGroupPrefix(inf, "虎牙") + "\n")
		// 虎牙: Python stream-proxy(19091) FLV 直通
		sb.WriteString("http://" + host + ":" + PUBLIC_PY_PORT + "/stream/huya/" + rid + "\n")
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
