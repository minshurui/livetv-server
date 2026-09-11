package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStringFlightGroupCombinesConcurrentLoads(t *testing.T) {
	var group stringFlightGroup
	var calls int32
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			got := group.do("huya:1", func() string {
				atomic.AddInt32(&calls, 1)
				time.Sleep(20 * time.Millisecond)
				return "stream-url"
			})
			if got != "stream-url" {
				t.Errorf("flight result=%q", got)
			}
		}()
	}
	close(start)
	workers.Wait()
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("loader called %d times, want 1", got)
	}
}
