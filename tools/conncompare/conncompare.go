package main

import (
	"crypto/md5"
	"encoding/binary"
	"flag"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// 与 livetv 完全一致的直连方式: 强制 tcp4 + UA Mozilla/5.0 + Referer
func fetch(url, ref string, seconds int) ([]string, error) {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.Dial("tcp4", addr)
		},
		DisableCompression: true,
	}
	client := &http.Client{Transport: transport, Timeout: time.Duration(seconds+5) * time.Second}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	if ref != "" {
		req.Header.Set("Referer", ref)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// 读 FLV header(9 bytes) + 首个 tag 检查
	head := make([]byte, 9)
	if _, err := io.ReadFull(resp.Body, head); err != nil {
		return nil, fmt.Errorf("read head: %v", err)
	}
	if string(head[:3]) != "FLV" {
		return nil, fmt.Errorf("not FLV: %q", head[:3])
	}
	lines := []string{fmt.Sprintf("FLV header OK, prev=%d", binary.BigEndian.Uint32(head[5:9]))}
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	n := 0
	buf := make([]byte, 11)
	for time.Now().Before(deadline) {
		if _, err := io.ReadFull(resp.Body, buf); err != nil {
			break
		}
		typ := buf[0]
		ds := int(buf[1])<<16 | int(buf[2])<<8 | int(buf[3])
		ts := uint32(buf[7])<<24 | uint32(buf[4])<<16 | uint32(buf[5])<<8 | uint32(buf[6])
		data := make([]byte, ds)
		if _, err := io.ReadFull(resp.Body, data); err != nil {
			break
		}
		// 读 prevTagSize
		prev := make([]byte, 4)
		if _, err := io.ReadFull(resp.Body, prev); err != nil {
			break
		}
		if typ == 9 && len(data) >= 2 {
			ft := (data[0] >> 4) & 0x0F
			codec := data[0] & 0x0F
			avc := data[1]
			extra := ""
			if avc == 0 { // 序列头
				extra = func() string { h := md5.Sum(data); return fmt.Sprintf("SPS(%d bytes) h=%x", len(data), h[:4]) }()
			} else if ft == 1 { // IDR
				extra = func() string { h := md5.Sum(data); return fmt.Sprintf("IDR h=%x", h[:4]) }()
			}
			lines = append(lines, fmt.Sprintf("t=%08d video ft=%d codec=%d avc=%d %s", ts, ft, codec, avc, extra))
			n++
			if n >= 14 {
				break
			}
		} else if typ == 8 && len(data) >= 2 {
			lines = append(lines, fmt.Sprintf("t=%08d audio cfg=%02x", ts, data[0]))
		} else if typ == 18 {
			lines = append(lines, fmt.Sprintf("t=%08d script(%d)", ts, ds))
		}
		if n >= 14 {
			break
		}
	}
	return lines, nil
}

func main() {
	url := flag.String("url", "", "CDN flv url")
	ref := flag.String("ref", "", "referer")
	sec := flag.Int("sec", 6, "capture seconds")
	flag.Parse()
	if *url == "" {
		fmt.Println("need -url")
		os.Exit(1)
	}
	lines, err := fetch(*url, *ref, *sec)
	if err != nil {
		fmt.Println("ERR:", err)
		os.Exit(1)
	}
	for _, l := range lines {
		fmt.Println("  ", l)
	}
}
