package main

import (
	"net/url"
	"testing"
	"time"
)

func TestExtractHuyaLinesFromStructuredPayload(t *testing.T) {
	oldRatio := HUYA_MAX_RATIO
	HUYA_MAX_RATIO = "2000"
	t.Cleanup(func() { HUYA_MAX_RATIO = oldRatio })

	page := `before stream: {"data":[{"gameStreamInfoList":[` +
		`{"sFlvUrl":"https://tx.flv.huya.com/src","sFlvUrlSuffix":"flv",` +
		`"sFlvAntiCode":"wsTime=695e06d2&fm=RFdxOEJjSjNoNkRKdDZUWV8kMF8kMV8kMl8kMw%3D%3D",` +
		`"sStreamName":"stream-name","sCdnType":"TX","lPresenterUid":1199627305549,` +
		`"iWebPriorityRate":80}` +
		`]}],"vMultiStreamInfo":[{"iBitRate":0},{"iBitRate":4000},{"iBitRate":2000},{"iBitRate":500}]} after`

	lines := extractHuyaLines(page)
	if len(lines) != 1 {
		t.Fatalf("lines=%d, want 1", len(lines))
	}
	got := lines[0]
	if got.base != "https://tx.flv.huya.com/src" || got.stream != "stream-name" || got.cdn != "TX" {
		t.Fatalf("unexpected line: %+v", got)
	}
	if got.uid != 1199627305549 || got.priority != 80 {
		t.Fatalf("uid/priority lost: %+v", got)
	}
	if !got.hasRatio || got.ratio != 2000 {
		t.Fatalf("ratio=%d hasRatio=%v, want 2000/true", got.ratio, got.hasRatio)
	}
}

func TestChooseHuyaRatio(t *testing.T) {
	payload := huyaStreamPayload{}
	for _, ratio := range []int{0, 4000, 500, 2000} {
		payload.MultiStreamInfo = append(payload.MultiStreamInfo, huyaMultiStreamInfo{BitRate: ratio})
	}
	for _, tc := range []struct {
		max  string
		want int
	}{
		{"2000", 2000},
		{"1500", 500},
		{"300", 500},
		{"0", 0},
		{"invalid", 2000},
	} {
		got, ok := chooseHuyaRatio(payload, tc.max)
		if !ok || got != tc.want {
			t.Errorf("max=%q: got %d/%v, want %d/true", tc.max, got, ok, tc.want)
		}
	}
}

func TestBuildHuyaAntiCodeCurrentAlgorithm(t *testing.T) {
	oldCodec := HUYA_CODEC
	HUYA_CODEC = "264"
	t.Cleanup(func() { HUYA_CODEC = oldCodec })

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	stream := "1199627305549-1199627305549-5718448156589424640-2399254734554-10057-A-0-1-imgplus"
	anti := "wsTime=695e06d2&fm=RFdxOEJjSjNoNkRKdDZUWV8kMF8kMV8kMl8kMw%3D%3D&ctype=huya_commserver&fs=gct&t=100"
	encoded, err := buildHuyaAntiCode(stream, anti, 1199627305549, 2000, true, now)
	if err != nil {
		t.Fatal(err)
	}
	q, err := url.ParseQuery(encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"wsSecret": "81d8f7abbfe27291d53eecffd5b4c7a9",
		"wsTime":   "695e06d2",
		"seqid":    "2966852905549",
		"ctype":    "huya_commserver",
		"ver":      "1",
		"fs":       "gct",
		"t":        "100",
		"codec":    "264",
		"ratio":    "2000",
		"u":        "1199839530319",
	}
	for key, value := range want {
		if q.Get(key) != value {
			t.Errorf("%s=%q, want %q", key, q.Get(key), value)
		}
	}
	if q.Get("fm") != "RFdxOEJjSjNoNkRKdDZUWV8kMF8kMV8kMl8kMw==" {
		t.Fatalf("fm was not preserved: %q", q.Get("fm"))
	}
}
