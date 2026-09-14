package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadIdleDeadline(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	c := &idleReadConn{Conn: left, idle: 30 * time.Millisecond}
	_, err := c.Read(make([]byte, 1))
	if e, ok := err.(net.Error); !ok || !e.Timeout() {
		t.Fatalf("want idle timeout, got %v", err)
	}
	go func() { _, _ = right.Write([]byte("x")) }()
	if _, err := c.Read(make([]byte, 1)); err != nil {
		t.Fatalf("deadline did not reset: %v", err)
	}
}

func TestPrefetchExpiresAndIsConsumedOnce(t *testing.T) {
	now := time.Now()
	for _, d := range []time.Duration{-time.Second, 0, time.Second} {
		entry := cacheEntry{val: "url", expire: now.Add(d)}
		got := takeFreshPrefetch(&entry, now)
		if (got != "") != (d > 0) {
			t.Fatalf("expiry %v: %q", d, got)
		}
		if takeFreshPrefetch(&entry, now) != "" {
			t.Fatal("prefetch reused twice")
		}
	}
}

func syntheticFLV() []byte {
	b := []byte{'F', 'L', 'V', 1, 5, 0, 0, 0, 9, 0, 0, 0, 0}
	for i := 0; i < 150; i++ {
		b = append(b, mkVideoTag(uint32(i*40), true, i)...)
	}
	return b
}

func TestProxyRefreshes403AndCancelsUpstream(t *testing.T) {
	for _, platform := range []string{"huya", "douyu"} {
		t.Run(platform, func(t *testing.T) {
			upstreamClosed := make(chan struct{})
			var badCalls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/old" {
					badCalls.Add(1)
					w.WriteHeader(403)
					return
				}
				_, _ = w.Write(syntheticFLV())
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(upstreamClosed)
			}))
			defer up.Close()
			var freshCalls atomic.Int32
			h := proxyHandler{resolve: func(ctx context.Context, p, rid string, fresh bool) string {
				if fresh {
					freshCalls.Add(1)
					return up.URL + "/new"
				}
				return up.URL + "/old"
			}}
			proxy := httptest.NewServer(h)
			defer proxy.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "GET", proxy.URL+"/stream/"+platform+"/123", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			first := make([]byte, 13)
			_, err = io.ReadFull(resp.Body, first)
			cancel()
			resp.Body.Close()
			if err != nil || !bytes.HasPrefix(first, []byte("FLV")) || resp.StatusCode != 200 {
				t.Fatalf("fresh retry failed: %v %q status %d", err, first, resp.StatusCode)
			}
			select {
			case <-upstreamClosed:
			case <-time.After(time.Second):
				t.Fatal("upstream survives channel switch")
			}
			if badCalls.Load() != 1 || freshCalls.Load() < 1 {
				t.Fatal("403 did not refresh URL")
			}
		})
	}
}

func TestProxySecondFailureReturns502(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer up.Close()
	h := proxyHandler{resolve: func(context.Context, string, string, bool) string { return up.URL }}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/stream/huya/123", nil))
	if w.Code != 502 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatalf("got %d %v", w.Code, w.Header())
	}
}

func TestProxyReconnectsAfterReadIdle(t *testing.T) {
	oldClient := httpClientV4
	transport := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &idleReadConn{Conn: conn, idle: 60 * time.Millisecond}, nil
	}}
	httpClientV4 = &http.Client{Transport: transport}
	defer func() { httpClientV4 = oldClient; transport.CloseIdleConnections() }()
	var requests atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = w.Write(syntheticFLV())
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer up.Close()
	resolver := func(context.Context, string, string, bool) string { return up.URL }
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w := httptest.NewRecorder()
	openAndServe(ctx, w, "huya", "123", up.URL, resolver)
	if requests.Load() < 2 {
		t.Fatalf("idle stream did not reconnect: requests=%d", requests.Load())
	}
	if bytes.Count(w.Body.Bytes(), []byte("FLV")) != 1 {
		t.Fatal("reconnect duplicated FLV header")
	}
}

func TestCanceledPlaybackDoesNotResolveOrWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if resolvePlayback(ctx, "huya", "123", true) != "" {
		t.Fatal("canceled resolve returned URL")
	}
	if waitPlayback(ctx, time.Hour) {
		t.Fatal("canceled retry continued")
	}
}
