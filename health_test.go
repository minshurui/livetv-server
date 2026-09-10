package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHealthCheckKeepsWhitelistWhenChannelsMissing(t *testing.T) {
	tmp := t.TempDir()
	oldChannels, oldAlive, oldStamp, oldLog := ChannelsF, AliveF, StampF, LogDir
	ChannelsF = filepath.Join(tmp, "missing-channels.json")
	AliveF = filepath.Join(tmp, "douyu-alive.txt")
	StampF = filepath.Join(tmp, "douyu-health.stamp")
	LogDir = tmp
	t.Cleanup(func() {
		ChannelsF, AliveF, StampF, LogDir = oldChannels, oldAlive, oldStamp, oldLog
	})

	const existing = "12345\n"
	if err := os.WriteFile(AliveF, []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	healthRunning = 0
	runHealthCheck("test")

	got, err := os.ReadFile(AliveF)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != existing {
		t.Fatalf("whitelist changed: %q", got)
	}
	if _, err := os.Stat(StampF); !os.IsNotExist(err) {
		t.Fatalf("empty health check unexpectedly wrote stamp: %v", err)
	}
}
