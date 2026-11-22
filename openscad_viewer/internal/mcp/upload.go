package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
)

// uploadTimeout bounds a fetch of an image by address.
const uploadTimeout = 30 * time.Second

// imageFromArgs reads the file for upload_image: exactly one of data (base64) and url, and a
// name. The name defaults to the file name in the URL, and to one with the right extension
// for the bytes when the URL has none.
func imageFromArgs(ctx context.Context, a args) ([]byte, string, error) {
	raw, _ := a["data"].(string)
	src := a.str("url")
	switch {
	case raw != "" && src != "":
		return nil, "", errors.New("give the file as data or as url, not both")
	case raw == "" && src == "":
		return nil, "", errors.New("give the file as data (base64 encoded) or as url")
	case src != "":
		b, err := fetchImage(ctx, src)
		if err != nil {
			return nil, "", err
		}
		name := a.str("name")
		if name == "" {
			name = nameFromURL(src)
		}
		return b, withExtension(name, b), nil
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, "", fmt.Errorf("data is not valid base64: %w", err)
	}
	name := a.str("name")
	if name == "" {
		return nil, "", errors.New("name is required with data: a file name ending in .png, .jpg or .jpeg")
	}
	return b, name, nil
}

// fetchImage downloads an image from an http or https address, within the time and size
// limits of an upload. It does not decode the bytes; UploadImage does that.
func fetchImage(ctx context.Context, src string) ([]byte, error) {
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("url %q: only http and https addresses can be fetched", src)
	}
	ctx, cancel := context.WithTimeout(ctx, uploadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, fmt.Errorf("url %q: %w", src, err)
	}
	req.Header.Set("User-Agent", "openscad-viewer")
	res, err := (&http.Client{Timeout: uploadTimeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", src, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", src, res.Status)
	}
	if res.ContentLength > project.MaxImageBytes {
		return nil, fmt.Errorf("%s is %d MB, the limit is %d MB: %w", src, res.ContentLength>>20, project.MaxImageBytes>>20, project.ErrTooLarge)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, project.MaxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", src, err)
	}
	if len(b) > project.MaxImageBytes {
		return nil, fmt.Errorf("%s is over the %d MB limit: %w", src, project.MaxImageBytes>>20, project.ErrTooLarge)
	}
	return b, nil
}

// nameFromURL is the last part of the address's path, or "image".
func nameFromURL(src string) string {
	u, err := url.Parse(src)
	if err != nil {
		return "image"
	}
	base := path.Base(u.Path)
	if base == "/" || base == "." || base == "" {
		return "image"
	}
	return base
}

// withExtension gives name a .png, .jpg or .jpeg extension when it has none, from what the
// bytes are, so the stored file is served with the right type.
func withExtension(name string, b []byte) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".png", ".jpg", ".jpeg":
		return name
	}
	if http.DetectContentType(b) == "image/jpeg" {
		return name + ".jpg"
	}
	return name + ".png"
}
