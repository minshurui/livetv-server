package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

func nowStr() string { return time.Now().Format("2006-01-02 15:04:05") }

// trimURL: 日志用, 截断超长签名 URL
func trimURL(u string) string {
	if len(u) > 160 {
		return u[:160] + "..."
	}
	return u
}

func sprintf(format string, a ...interface{}) string { return fmt.Sprintf(format, a...) }

func stderrPrintf(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, format, a...)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func parseFloat(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

func itoa(n int) string { return strconv.Itoa(n) }
