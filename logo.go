package main

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maxLogoBytes = 2 << 20

var logoIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
var logoFlights stringFlightGroup

func extinfLogo(inf string) string {
	const marker = `tvg-logo="`
	start := strings.Index(inf, marker)
	if start < 0 {
		return ""
	}
	rest := inf[start+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func channelLogoSource(platform, rid string) string {
	for _, entry := range loadChannels()[platform] {
		if entry[0] == rid {
			return extinfLogo(entry[1])
		}
	}
	return ""
}

func allowedLogoSource(platform string, source *url.URL) bool {
	if source == nil || source.Scheme != "https" || source.User != nil || source.Port() != "" {
		return false
	}
	host := strings.ToLower(source.Hostname())
	switch platform {
	case "huya":
		return host == "huyaimg.msstatic.com" || host == "anchorpost.msstatic.com"
	case "douyu":
		return host == "apic.douyucdn.cn" || host == "rpic.douyucdn.cn"
	default:
		return false
	}
}

func serveCachedLogo(w http.ResponseWriter, r *http.Request, path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxLogoBytes {
		return false
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, path)
	return true
}

func downloadLogo(platform, sourceText, target string) error {
	source, err := url.Parse(sourceText)
	if err != nil || !allowedLogoSource(platform, source) {
		return fmt.Errorf("logo source is not allowed")
	}
	client := newV4Client(15 * time.Second)
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !allowedLogoSource(platform, req.URL) {
			return fmt.Errorf("logo redirect is not allowed")
		}
		return nil
	}
	req, err := http.NewRequest(http.MethodGet, source.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UA_PC)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("logo response status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLogoBytes+1))
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > maxLogoBytes {
		return fmt.Errorf("logo size %d is invalid", len(data))
	}
	if contentType := http.DetectContentType(data); !strings.HasPrefix(contentType, "image/") {
		return fmt.Errorf("logo content type %s is invalid", contentType)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".logo-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0644); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}

func handleLogo(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/logo/"), "/")
	if len(parts) != 2 || (parts[0] != "huya" && parts[0] != "douyu") || !logoIDRe.MatchString(parts[1]) {
		http.Error(w, "invalid logo path", http.StatusBadRequest)
		return
	}
	platform, rid := parts[0], parts[1]
	target := filepath.Join(LogosRoot, platform, rid+".img")
	if serveCachedLogo(w, r, target) {
		return
	}
	result := logoFlights.do(target, func() string {
		if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return ""
		}
		if err := downloadLogo(platform, channelLogoSource(platform, rid), target); err != nil {
			return err.Error()
		}
		return ""
	})
	if result != "" {
		logf("[logo] %s/%s: %s", platform, rid, result)
		http.Error(w, "logo unavailable", http.StatusBadGateway)
		return
	}
	if !serveCachedLogo(w, r, target) {
		http.Error(w, "logo cache unavailable", http.StatusInternalServerError)
	}
}
