package main

import (
	"bytes"
	"fmt"
	"testing"
)

// 合成 FLV video tag: 返回 header+data(+4 prevTagSize), 完整 11+ds+4 字节
func mkVideoTag(ts uint32, isIDR bool, seq int) []byte {
	ds := 20
	b := make([]byte, 11+ds+4)
	b[0] = 9
	b[1] = byte(ds >> 16)
	b[2] = byte(ds >> 8)
	b[3] = byte(ds)
	b[4] = byte(ts >> 16)
	b[5] = byte(ts >> 8)
	b[6] = byte(ts)
	b[7] = byte(ts >> 24)
	if isIDR {
		b[11] = 0x17
	} else {
		b[11] = 0x27
	}
	b[12] = 1 // NALU
	b[13] = byte(seq)
	// prevTagSize
	pts := 11 + ds
	b[11+ds] = byte(pts >> 24)
	b[11+ds+1] = byte(pts >> 16)
	b[11+ds+2] = byte(pts >> 8)
	b[11+ds+3] = byte(pts)
	return b
}

func mkAudioTag(ts uint32, isSeq bool) []byte {
	ds := 6
	b := make([]byte, 11+ds+4)
	b[0] = 8
	b[1] = byte(ds >> 16)
	b[2] = byte(ds >> 8)
	b[3] = byte(ds)
	b[4] = byte(ts >> 16)
	b[5] = byte(ts >> 8)
	b[6] = byte(ts)
	b[7] = byte(ts >> 24)
	if isSeq {
		b[11] = 0xAF // AAC seq header (高4位=10 AAC, 低4位=0)
	} else {
		b[11] = 0xAF // AAC raw... 低4位非0才是raw; 简化用 0xAF 不行, 用 0xA1
	}
	if !isSeq {
		b[11] = 0xA1 // soundFormat=10(AAC), 其余非0 → raw frame
	}
	pts := 11 + ds
	b[11+ds] = byte(pts >> 24)
	b[11+ds+1] = byte(pts >> 16)
	b[11+ds+2] = byte(pts >> 8)
	b[11+ds+3] = byte(pts)
	return b
}

func TestReconnectMonotonic(t *testing.T) {
	var in bytes.Buffer
	baseTS := uint32(98700 * 1000)
	// 新连接: script(18) → audio seq → 非IDR video → audio → IDR → 后续
	s := make([]byte, 15)
	s[0] = 18
	in.Write(s)
	in.Write(mkAudioTag(baseTS+5, true))
	in.Write(mkVideoTag(baseTS+10, false, 1))
	in.Write(mkVideoTag(baseTS+40, false, 2))
	in.Write(mkAudioTag(baseTS+40, false))
	in.Write(mkVideoTag(baseTS+80, true, 3)) // IDR
	in.Write(mkVideoTag(baseTS+120, false, 4))
	in.Write(mkAudioTag(baseTS+120, false))

	var out bytes.Buffer
	sw := newFLVStreamWriter(&in, &out, nil, baseTS, true)
	sw.pump()
	b := out.Bytes()
	if len(b) == 0 {
		t.Fatalf("无输出")
	}
	// 解析输出 tag
	type tv struct {
		typ byte
		ts  uint32
	}
	var tags []tv
	i := 0
	for i+11 <= len(b) {
		typ := b[i]
		ds := int(b[i+1])<<16 | int(b[i+2])<<8 | int(b[i+3])
		ts := uint32(b[i+7])<<24 | uint32(b[i+4])<<16 | uint32(b[i+5])<<8 | uint32(b[i+6])
		tags = append(tags, tv{typ, ts})
		i += 11 + ds + 4
	}
	if tags[0].typ != 9 {
		t.Fatalf("首tag type=%d, 应为9(视频IDR)", tags[0].typ)
	}
	if tags[0].ts <= baseTS {
		t.Fatalf("首视频 ts=%d 应>baseTS=%d", tags[0].ts, baseTS)
	}
	// 视频单调
	var lv uint32
	for _, g := range tags {
		if g.typ == 9 {
			if lv != 0 && g.ts <= lv {
				t.Fatalf("视频不单调 %d→%d", lv, g.ts)
			}
			lv = g.ts
		}
	}
	// 音频单调
	var la uint32
	for _, g := range tags {
		if g.typ == 8 {
			if la != 0 && g.ts <= la {
				t.Fatalf("音频不单调 %d→%d", la, g.ts)
			}
			la = g.ts
		}
	}
	fmt.Printf("PASS: %d tags 首tag=video ts=%d>base=%d | 视频末=%d 音频末=%d\n",
		len(tags), tags[0].ts, baseTS, lv, la)
}
