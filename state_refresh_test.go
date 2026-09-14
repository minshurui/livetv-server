package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlaybackRedirectTracksOfflineAndRestart(t *testing.T) {
	for _, platform := range []string{"huya", "douyu"} {
		t.Run(platform, func(t *testing.T) {
			state := ""
			freshSeen := false
			h := &aioHandler{resolve: func(ctx context.Context, p, rid string, fresh bool) string {
				freshSeen = fresh
				return state
			}}
			for _, target := range []string{"https://cdn.example/first.flv", "", "https://cdn.example/restarted.flv"} {
				state = target
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", "/"+platform+"/123?fresh=1", nil))
				want := http.StatusFound
				if target == "" {
					want = http.StatusNotFound
				}
				if w.Code != want || w.Header().Get("Location") != target || !freshSeen {
					t.Fatalf("state %q: code=%d location=%q fresh=%v", target, w.Code, w.Header().Get("Location"), freshSeen)
				}
				if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
					t.Fatal("response may be cached")
				}
			}
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/"+platform+"/123?notfresh=1", nil))
			if freshSeen {
				t.Fatal("unrelated parameter triggered fresh resolve")
			}
		})
	}
}

func TestLinePlaylistsCannotBeCached(t *testing.T) {
	old := ChannelsF
	ChannelsF = filepath.Join(t.TempDir(), "channels.json")
	t.Cleanup(func() { ChannelsF = old })
	for _, path := range []string{"/huyayqk.m3u", "/douyuyqk.m3u", "/yylunbo.m3u"} {
		w := httptest.NewRecorder()
		(&aioHandler{}).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatal(path)
		}
	}
}

func TestWhitelistBoundToCatalogAndObservationTime(t *testing.T) {
	oldAlive := AliveF
	AliveF = filepath.Join(t.TempDir(), "alive.txt")
	t.Cleanup(func() { AliveF = oldAlive })
	ch := channelsFile{"douyu": {{"1", "first"}, {"2", "offline"}}}
	snapshot := aliveSnapshot{Catalog: catalogVersion(ch), CheckedAt: time.Now().Unix(), Alive: []string{"1"}}
	writeAliveSnapshot(snapshot)
	set := aliveWhitelistFor(ch)
	if set == nil || !set["1"] || set["2"] {
		t.Fatalf("incorrect fresh whitelist: %v", set)
	}
	updated := channelsFile{"douyu": {{"1", "first"}, {"2", "offline"}, {"3", "new live room"}}}
	if got := aliveWhitelistFor(updated); got != nil {
		t.Fatal("old whitelist hides new room")
	}
	for _, ts := range []int64{time.Now().Unix() - ALIVE_MAXAGE, time.Now().Unix() + 60} {
		snapshot.CheckedAt = ts
		writeAliveSnapshot(snapshot)
		if got := aliveWhitelistFor(ch); got != nil {
			t.Fatal("expired/future observation used to hide rooms")
		}
	}
	if err := os.WriteFile(AliveF+".json", []byte("partial"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := aliveWhitelistFor(ch); got != nil {
		t.Fatal("broken snapshot should not hide rooms")
	}
}

func TestTTLCacheRefreshesExpiredPositiveAndNegative(t *testing.T) {
	c := newTTLCache()
	for _, val := range []string{"old-url", ""} {
		c.set("room", val, 30)
		if got, ok := c.get("room"); !ok || got != val {
			t.Fatal("cache miss before expiry")
		}
		entry := c.m["room"]
		if val == "" && time.Until(entry.expire) > FAIL_TTL*time.Second {
			t.Fatal("negative TTL too long")
		}
		entry.expire = time.Now().Add(-time.Second)
		c.m["room"] = entry
		if _, ok := c.get("room"); ok {
			t.Fatal("expired first state retained")
		}
		c.set("room", "new-url", 30)
		if got, _ := c.get("room"); got != "new-url" {
			t.Fatal("failed to recover")
		}
	}
}
