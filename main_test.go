package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReqHost(t *testing.T) {
	tests := []struct {
		host string
		want string
	}{
		{"example.com:8081", "example.com"},
		{"192.0.2.10:8081", "192.0.2.10"},
		{"[2001:db8::1]:8081", "[2001:db8::1]"},
		{"2001:db8::1", "[2001:db8::1]"},
	}
	for _, tt := range tests {
		r := httptest.NewRequest("GET", "http://service/allinone.m3u", nil)
		r.Host = tt.host
		if got := reqHost(r); got != tt.want {
			t.Errorf("reqHost(%q) = %q, want %q", tt.host, got, tt.want)
		}
	}
}

func TestReqHostIPv6Fallback(t *testing.T) {
	old := PUBLIC_HOST
	PUBLIC_HOST = "2001:db8::2"
	t.Cleanup(func() { PUBLIC_HOST = old })
	r := httptest.NewRequest("GET", "http://service/allinone.m3u", nil)
	r.Host = ""
	if got := reqHost(r); got != "[2001:db8::2]" {
		t.Fatalf("reqHost fallback = %q", got)
	}
}

func TestValidHLSFilename(t *testing.T) {
	for _, name := range []string{"index.m3u8", "seg_00001.ts", "seg_9.ts"} {
		if !validHLSFilename(name) {
			t.Errorf("expected valid filename: %s", name)
		}
	}
	for _, name := range []string{"ffmpeg.err", "../index.m3u8", "seg_.ts", "seg_one.ts"} {
		if validHLSFilename(name) {
			t.Errorf("expected invalid filename: %s", name)
		}
	}
}

func TestAggregateM3UUsesPublicStreamPorts(t *testing.T) {
	tmp := t.TempDir()
	oldChannels, oldAlive, oldStamp, oldTV := ChannelsF, AliveF, StampF, TvF
	oldProxy, oldPy := PUBLIC_PROXY_PORT, PUBLIC_PY_PORT
	ChannelsF = filepath.Join(tmp, "channels.json")
	AliveF = filepath.Join(tmp, "missing-alive.txt")
	StampF = filepath.Join(tmp, "missing-stamp")
	TvF = filepath.Join(tmp, "missing-tv.m3u")
	PUBLIC_PROXY_PORT = "29090"
	PUBLIC_PY_PORT = "29091"
	t.Cleanup(func() {
		ChannelsF, AliveF, StampF, TvF = oldChannels, oldAlive, oldStamp, oldTV
		PUBLIC_PROXY_PORT, PUBLIC_PY_PORT = oldProxy, oldPy
	})
	content := `{"huya":[["1","#EXTINF:-1 group-title=\"测试\",虎牙"]],"douyu":[["2","#EXTINF:-1 group-title=\"测试\",斗鱼"]]}`
	if err := os.WriteFile(ChannelsF, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got := aggregateM3U("example.com")
	for _, want := range []string{
		"http://example.com:29090/stream/huya/1",
		"http://example.com:29090/stream/douyu/2",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("aggregate M3U missing %q:\n%s", want, got)
		}
	}
}
