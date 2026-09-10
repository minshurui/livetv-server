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
