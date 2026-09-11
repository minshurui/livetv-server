package main

import (
	"net/url"
	"testing"
	"time"
)

func TestExtractHuyaLinesFromStructuredPayload(t *testing.T) {
	page := `before stream: {"data":[{"gameStreamInfoList":[` +
		`{"sFlvUrl":"https://tx.flv.huya.com/src","sFlvUrlSuffix":"flv",` +
		`"sFlvAntiCode":"wsTime=695e06d2&fm=RFdxOEJjSjNoNkRKdDZUWV8kMF8kMV8kMl8kMw%3D%3D",` +
		`"sStreamName":"stream-name","sCdnType":"TX","lPresenterUid":1199627305549,` +
		`"iWebPriorityRate":80}` +
		`]}]} after`

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
}

func TestBuildHuyaAntiCodeCurrentAlgorithm(t *testing.T) {
	oldCodec := HUYA_CODEC
	HUYA_CODEC = "264"
	t.Cleanup(func() { HUYA_CODEC = oldCodec })

	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	stream := "1199627305549-1199627305549-5718448156589424640-2399254734554-10057-A-0-1-imgplus"
	anti := "wsTime=695e06d2&fm=RFdxOEJjSjNoNkRKdDZUWV8kMF8kMV8kMl8kMw%3D%3D&ctype=huya_commserver&fs=gct&t=100"
	encoded, err := buildHuyaAntiCode(stream, anti, 1199627305549, now)
	if err != nil {
		t.Fatal(err)
	}
	q, err := url.ParseQuery(encoded)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"wsSecret": "b3ec8ed63eb0dae0ffa0ab35461d1fe3",
		"wsTime":   "695e06d2",
		"seqid":    "2966852905549",
		"ctype":    "huya_commserver",
		"ver":      "1",
		"fs":       "gct",
		"t":        "100",
		"codec":    "264",
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
