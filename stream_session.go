package main

import (
	"context"
	"net"
	"time"
)

// 每次网络读取重新设置空闲期限；持续有数据的长直播不会被总超时切断。
type idleReadConn struct {
	net.Conn
	idle time.Duration
}

func (c *idleReadConn) Read(p []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	return c.Conn.Read(p)
}

type streamResolver func(context.Context, string, string, bool) string

// 共享签名解析仍受 TIMEOUT_S 限制；取消某个观众只停止该观众等待，
// 不中止其他观众可能共用的解析。视频 CDN 请求使用观众的 context。
func resolvePlayback(ctx context.Context, platform, rid string, fresh bool) string {
	if ctx.Err() != nil {
		return ""
	}
	resolve := resolverFor(platform)
	if resolve == nil {
		return ""
	}
	result := make(chan string, 1)
	go func() {
		if fresh {
			delCacheFor(platform, rid)
		}
		result <- resolve(rid)
	}()
	select {
	case <-ctx.Done():
		return ""
	case u := <-result:
		return u
	}
}

func waitPlayback(ctx context.Context, delay time.Duration) bool {
	t := time.NewTimer(delay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func playbackTTL(platform string) time.Duration {
	ttl := 30
	if platform == "huya" {
		ttl = HUYA_CACHE_TTL
	}
	if platform == "douyu" {
		ttl = DOUYU_CACHE_TTL
	}
	return time.Duration(ttl) * time.Second
}

func takeFreshPrefetch(entry *cacheEntry, now time.Time) string {
	value := entry.val
	if !now.Before(entry.expire) {
		value = ""
	}
	*entry = cacheEntry{}
	return value
}
