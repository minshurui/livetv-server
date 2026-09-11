package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAllowedLogoSource(t *testing.T) {
	tests := []struct {
		platform string
		raw      string
		want     bool
	}{
		{"huya", "https://huyaimg.msstatic.com/avatar/a.jpg", true},
		{"huya", "https://anchorpost.msstatic.com/cdnimage/a.jpg", true},
		{"douyu", "https://apic.douyucdn.cn/upload/a.jpg", true},
		{"douyu", "https://rpic.douyucdn.cn/live-cover/a.jpg", true},
		{"huya", "http://huyaimg.msstatic.com/avatar/a.jpg", false},
		{"huya", "https://evil.example/a.jpg", false},
		{"huya", "https://huyaimg.msstatic.com.evil.example/a.jpg", false},
	}
	for _, test := range tests {
		u, _ := url.Parse(test.raw)
		if got := allowedLogoSource(test.platform, u); got != test.want {
			t.Errorf("allowedLogoSource(%q, %q)=%v, want %v", test.platform, test.raw, got, test.want)
		}
	}
}

func TestLocalizeChannelLogoUsesStableRoomURL(t *testing.T) {
	oldPort := PUBLIC_LIVETV_PORT
	PUBLIC_LIVETV_PORT = "8201"
	t.Cleanup(func() { PUBLIC_LIVETV_PORT = oldPort })
	inf := `#EXTINF:-1 tvg-logo="https://huyaimg.msstatic.com/avatar/a.jpg?x=1" group-title="一起看",主播`
	got := localizeChannelLogo(inf, "huya", "123", "192.0.2.8")
	if !strings.Contains(got, `tvg-logo="http://192.0.2.8:8201/logo/huya/123"`) {
		t.Fatalf("localized logo missing: %s", got)
	}
	if strings.Contains(got, "src=") {
		t.Fatalf("logo URL should remain stable when upstream avatar changes: %s", got)
	}
}

func TestHandleLogoServesPersistentCacheWithoutSourceRequest(t *testing.T) {
	oldRoot := LogosRoot
	LogosRoot = t.TempDir()
	t.Cleanup(func() { LogosRoot = oldRoot })
	dir := filepath.Join(LogosRoot, "huya")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte("\x89PNG\r\n\x1a\ncache")
	if err := os.WriteFile(filepath.Join(dir, "123.img"), data, 0644); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/logo/huya/123", nil)
	recorder := httptest.NewRecorder()
	handleLogo(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.String() != string(data) {
		t.Fatalf("unexpected cached logo body")
	}
	if !strings.Contains(recorder.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("missing immutable cache header: %v", recorder.Header())
	}
}
