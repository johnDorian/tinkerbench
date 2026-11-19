package userparts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// MaxDownload is the largest bundle Fetch accepts.
const MaxDownload = 20 << 20

var githubBlob = regexp.MustCompile(`^/([^/]+)/([^/]+)/blob/(.+)$`)

// NormalizeURL turns a GitHub "blob" page link into the raw file link; other URLs are unchanged.
func NormalizeURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q is not an http(s) URL", raw)
	}
	if u.Host == "github.com" {
		if m := githubBlob.FindStringSubmatch(u.Path); m != nil {
			return "https://raw.githubusercontent.com/" + m[1] + "/" + m[2] + "/" + m[3], nil
		}
	}
	return u.String(), nil
}

// blockLAN refuses connections to this machine and the local network, so a link given to the
// server cannot be used to reach Home Assistant, the NAS or the router.
func blockLAN(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("refusing %s", address)
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("refusing to fetch from a local address (%s)", ip)
	}
	return nil
}

// Fetch downloads a bundle from a public http(s) URL.
func Fetch(ctx context.Context, raw string) ([]byte, error) {
	return fetch(ctx, raw, blockLAN)
}

func fetch(ctx context.Context, raw string, control func(network, address string, c syscall.RawConn) error) ([]byte, error) {
	link, err := NormalizeURL(raw)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 10 * time.Second, Control: control}).DialContext,
			ResponseHeaderTimeout: 15 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "fritzing-workbench")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", link, err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("download %s: %s", link, res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxDownload+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxDownload {
		return nil, fmt.Errorf("download %s: larger than %d MB", link, MaxDownload>>20)
	}
	return b, nil
}
