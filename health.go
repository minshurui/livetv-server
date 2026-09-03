package main

// 斗鱼健康检查 v2 → Go goroutine (替代 douyu-health.sh + cron)
// 阶段1: 全量解析每个 douyu rid → 筛出能解析出真实 douyucdn FLV 的候选
// 阶段2: 候选并行实测拉流(4s, >200KB 才存活)
// 输出: douyu-alive.txt + .douyu-health.stamp
// 触发: 启动时 + 每 HealthIntervalS + m3u 拉取发现白名单旧(>ALIVE_TRIGGER)
// 防重叠: atomic 锁 (Python 版 pgrep 锁)

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const pullOKBytes = 200000 // >200KB/4s 才算真活
const pullSeconds = 4

var healthRunning int32
var healthMu sync.Mutex
var healthLastRun int64 // unix

func isHealthRunning() bool { return atomic.LoadInt32(&healthRunning) == 1 }

// maybeTriggerHealth: m3u 拉取时调用; 白名单旧/缺失则后台触发(防重叠)
func maybeTriggerHealth() {
	age := aliveAgeSec()
	if age < 0 || age >= ALIVE_TRIGGER {
		go runHealthCheck("m3u-trigger")
	}
}

// runHealthCheck: 全量双阶段健康检查 (防重叠)
func runHealthCheck(reason string) {
	if !atomic.CompareAndSwapInt32(&healthRunning, 0, 1) {
		logf("[health] 已有实例在跑, 跳过 (%s)", reason)
		return
	}
	defer atomic.StoreInt32(&healthRunning, 0)
	healthMu.Lock()
	healthLastRun = time.Now().Unix()
	healthMu.Unlock()

	start := time.Now()
	logf("[health] === 斗鱼健康检查开始 (%s) ===", reason)
	ch := loadChannels()
	entries := ch["douyu"]
	rids := make([]string, 0, len(entries))
	for _, e := range entries {
		if e[0] != "" {
			rids = append(rids, e[0])
		}
	}
	// 随机顺序(打散)
	rng := newRand()
	for i := len(rids) - 1; i > 0; i-- {
		j := rng.Intn(i + 1)
		rids[i], rids[j] = rids[j], rids[i]
	}

	// ---------- 阶段1: 串行解析(带冷却), 筛候选 ----------
	total := len(rids)
	logf("[health] 阶段1 全量解析 %d rid", total)
	var candidates []string
	for i, rid := range rids {
		// 解析判定: resolveDouyu 返回真实 douyucdn FLV = 候选
		u := resolveDouyuFresh(rid)
		if isRealStream(u) {
			candidates = append(candidates, rid)
		} else {
			// fresh 复测一次(规避限流瞬时假象) — Python: sleep 0.2 + fresh
			time.Sleep(120 * time.Millisecond)
			u2 := resolveDouyuFresh(rid)
			if isRealStream(u2) {
				candidates = append(candidates, rid)
			}
		}
		// 限速 100-200ms
		time.Sleep(time.Duration(100+rng.Intn(100)) * time.Millisecond)
		if (i+1)%100 == 0 {
			logf("[health]     阶段1 进度 %d/%d, 候选 %d", i+1, total, len(candidates))
		}
	}
	logf("[health] 阶段1 完成: 候选 %d rid", len(candidates))

	// ---------- 阶段2: 候选并行实测拉流 ----------
	alive := make([]string, 0, len(candidates))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, rid := range candidates {
		wg.Add(1)
		sem <- struct{}{}
		go func(rid string) {
			defer wg.Done()
			defer func() { <-sem }()
			if pullTestOK(rid) {
				mu.Lock()
				alive = append(alive, rid)
				mu.Unlock()
			}
		}(rid)
	}
	wg.Wait()
	sort.Strings(alive)

	// 落盘
	writeAlive(alive)
	logf("[health] === 完成: 真可播 %d/%d, 用时 %.0fs ===", len(alive), total, time.Since(start).Seconds())
}

// resolveDouyuFresh: 绕过缓存强制重新解析
func resolveDouyuFresh(rid string) string {
	douyuCache.del("douyu:" + rid)
	return resolveDouyu(rid)
}

func isRealStream(u string) bool {
	if u == "" {
		return false
	}
	low := strings.ToLower(u)
	if strings.Contains(low, "jsdelivr") || strings.Contains(low, "testvideo") ||
		strings.Contains(low, "feiyang666999") {
		return false
	}
	return true
}

// pullTestOK: 经 stream-proxy 同款链路实测拉流 4s, 收 >200KB 判活
// (与 douyu-health.sh 阶段2 一致: 走 19090 转发判定)
func pullTestOK(rid string) bool {
	url := "http://127.0.0.1:" + PROXY_PORT + "/stream/douyu/" + rid
	body, status, bytes, err := fetchFirstBytes(url, pullSeconds)
	if err != nil {
		return false
	}
	if status != 200 || bytes <= pullOKBytes {
		// 日志收敛: 仅记录非正常形态
		if status == 200 && bytes <= pullOKBytes {
			logf("[health]    假活剔除 %s: 200 仅 %dB (占位流)", rid, bytes)
		}
		return false
	}
	if len(body) >= 4 && string(body[:3]) == "FLV" {
		return true
	}
	// 非 FLV 开头仍可能正常(douyu 流直接转发), 以字节数判定
	return true
}

func writeAlive(rids []string) {
	var sb strings.Builder
	for _, r := range rids {
		sb.WriteString(r + "\n")
	}
	tmp := AliveF + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0644); err != nil {
		logf("[health] 写 %s 失败: %v", tmp, err)
		return
	}
	_ = os.Rename(tmp, AliveF)
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	_ = os.WriteFile(StampF, []byte(stamp), 0644)
	logf("[health] 白名单 %d rid → %s", len(rids), AliveF)
}
