package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// 测量流的数据连续性: 记录数据包到达间隔, 找 >300ms 的缺口
func main() {
	url := flag.String("url", "", "url")
	sec := flag.Int("sec", 45, "duration")
	flag.Parse()
	if *url == "" {
		fmt.Println("need -url")
		os.Exit(1)
	}
	client := &http.Client{Timeout: time.Duration(*sec+10) * time.Second}
	resp, err := client.Get(*url)
	if err != nil {
		fmt.Println("ERR:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	fmt.Printf("HTTP %d CT=%s\n", resp.StatusCode, resp.Header.Get("Content-Type"))

	buf := make([]byte, 4096)
	last := time.Now()
	start := last
	gaps := 0
	total := 0
	var prevTS int64 = -1
	sameTS := 0
	first := true
	deadline := time.Now().Add(time.Duration(*sec) * time.Second)
	for time.Now().Before(deadline) {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			now := time.Now()
			dt := now.Sub(last)
			if !first && dt > 300*time.Millisecond {
				fmt.Printf("  [缺口 %.0fms] @ %d MB (%ds)\n", dt.Milliseconds(), total>>20, int(now.Sub(start).Seconds()))
				gaps++
			}
			first = false
			last = now
			total += n
			// 粗略找 FLV tag 时间戳(遍历缓冲找 09/08 类型头)
			_ = binary.BigEndian
			_ = prevTS
			_ = sameTS
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Println("READ ERR:", err)
			break
		}
	}
	fmt.Printf("总计: %d MB, %.0f 秒, 缺口≥300ms 共 %d 次\n", total>>20, time.Since(start).Seconds(), gaps)
}
