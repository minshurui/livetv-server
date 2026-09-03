package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	url := os.Args[1]
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", "https://www.huya.com/11274154")
	tr := &http.Transport{
		DialContext: func(c context.Context, n, a string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 10 * time.Second}).DialContext(c, "tcp4", a)
		},
		DisableCompression: true,
	}
	client := &http.Client{Transport: tr}
	resp, err := client.Do(req)
	if err != nil { fmt.Println("ERR", err); return }
	defer resp.Body.Close()
	fmt.Printf("HTTP %d CT=%s\n", resp.StatusCode, resp.Header.Get("Content-Type"))
	if resp.StatusCode != 200 { return }

	br := bufio.NewReaderSize(resp.Body, 256*1024)
	head := make([]byte, 13)
	if _, err := io.ReadFull(br, head); err != nil { fmt.Println("no FLV head:", string(head)); return }
	fmt.Printf("FLV header %q 流开始 %s\n", head[:3], time.Now().Format("15:04:05"))

	var lastTS uint32
	var lastKeyTS uint32
	var lastKeyTime time.Time
	var spsHash string
	n := 0
	keyInterval := 0
	for {
		hdr := make([]byte, 11)
		if _, err := io.ReadFull(br, hdr); err != nil {
			fmt.Printf("\n断开 @ %d tag, 最后ts=%d (%s)\n", n, lastTS, time.Now().Format("15:04:05"))
			return
		}
		size := int(hdr[1])<<16 | int(hdr[2])<<8 | int(hdr[3])
		ts := uint32(hdr[7])<<24 | uint32(hdr[4])<<16 | uint32(hdr[5])<<8 | uint32(hdr[6])
		data := make([]byte, size+4)
		if _, err := io.ReadFull(br, data); err != nil { fmt.Println("残缺tag结束"); return }
		n++

		if hdr[0] == 9 && size >= 5 {
			ft := data[0] >> 4
			avcPkt := data[1]
			if avcPkt == 0 { // AVCDecoderConfigurationRecord (SPS/PPS)
				// 提取 SPS (通常 offset 10 起, len byte 在 10)
				sh := ""
				if size >= 12 {
					sh = fmt.Sprintf("%x", data[10:minInt(size, 30)])
				}
				changed := ""
				if spsHash != "" && spsHash != sh { changed = " [SPS变化!]" }
				if sh != "" {
					fmt.Printf("  [SPS] tag#%d ts=%d 前40hex=%s%s\n", n, ts, sh, changed)
					spsHash = sh
				}
			} else if ft == 1 && avcPkt == 1 { // 关键帧
				if !lastKeyTime.IsZero() {
					keyInterval = int(time.Since(lastKeyTime).Seconds())
				}
				lastKeyTime = time.Now()
				if lastKeyTS != 0 && ts < lastKeyTS {
					fmt.Printf("  [关键帧TS回退] %d->%d @#%d\n", lastKeyTS, ts, n)
				}
				lastKeyTS = ts
			}
		}
		if hdr[0] == 8 && n <= 3 {
			fmt.Printf("  #%d audio hdr=%02x ts=%d\n", n, data[0], ts)
		}
		if n%500 == 0 {
			fmt.Printf("  ... #%d ts=%d key间隔=%ds\n", n, ts, keyInterval)
		}
		if ts < lastTS && hdr[0] != 18 {
			fmt.Printf("  [TS回退!] %d -> %d type=%d @#%d\n", lastTS, ts, hdr[0], n)
		}
		lastTS = ts
	}
}
func minInt(a, b int) int { if a < b { return a }; return b }
