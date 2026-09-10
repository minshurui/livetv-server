package main

import (
	"net/http/httptest"
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
