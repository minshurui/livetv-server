package main

import (
	"sync"
	"time"
)

type cacheEntry struct {
	val    string // "" 表示失败/无流
	expire time.Time
}

type ttlCache struct {
	mu sync.Mutex
	m  map[string]cacheEntry
}

func newTTLCache() *ttlCache {
	return &ttlCache{m: make(map[string]cacheEntry)}
}

// get: 命中且未过期返回 val + true
func (c *ttlCache) get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok || time.Now().After(e.expire) {
		if ok {
			delete(c.m, key)
		}
		return "", false
	}
	return e.val, true
}

// set: 成功按 ttl 秒缓存; 失败("")按 min(ttl, FAIL_TTL)
func (c *ttlCache) set(key, val string, ttl int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := time.Duration(ttl) * time.Second
	if val == "" {
		dd := minInt(ttl, FAIL_TTL)
		d = time.Duration(dd) * time.Second
	}
	c.m[key] = cacheEntry{val: val, expire: time.Now().Add(d)}
	// 简单清理: 超 4096 项时清过期
	if len(c.m) > 4096 {
		now := time.Now()
		for k, e := range c.m {
			if now.After(e.expire) {
				delete(c.m, k)
			}
		}
	}
}

func (c *ttlCache) del(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, key)
}
