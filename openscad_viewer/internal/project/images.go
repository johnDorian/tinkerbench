package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ImagesDir is the folder inside a project that holds uploaded scans and photos. They are
// inputs to measuring, not source, so they are not versioned: a .scad file is small and
// worth a copy per save, a 20 MB scan is not.
const ImagesDir = "images"

// MaxImageBytes is the largest upload accepted. A 1200 dpi scan of a palm-sized part is a
// few megabytes; the limit is there to stop a whole-bed scan filling the Pi's disk.
const MaxImageBytes = 32 << 20

var imageRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}\.(?i:png|jpe?g)$`)

// ErrTooLarge is returned for an upload over MaxImageBytes.
var ErrTooLarge = errors.New("too large")

// Image describes one uploaded image.
type Image struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// Width, Height (after EXIF rotation, as a photo viewer shows it), DPI, Camera and
	// DPIUsable are filled in by the caller that can decode the file; the store itself does
	// not look inside it. DPI is what the file records. It is a scale only when DPIUsable:
	// a camera writes 72 or 300 whatever it photographed.
	Width     int     `json:"width,omitempty"`
	Height    int     `json:"height,omitempty"`
	DPI       float64 `json:"dpi,omitempty"`
	Camera    string  `json:"camera,omitempty"` // "Make Model" from EXIF, when a camera took it
	DPIUsable bool    `json:"dpi_usable"`
}

// ValidImage reports whether file is a usable image name.
func ValidImage(file string) error {
	if !imageRe.MatchString(file) {
		return fmt.Errorf("invalid image name %q: letters, digits, ., - and _, ending in .png, .jpg or .jpeg", file)
	}
	return nil
}

// ContentType is the media type to serve an image with.
func ContentType(file string) string {
	if strings.HasSuffix(strings.ToLower(file), ".png") {
		return "image/png"
	}
	return "image/jpeg"
}

// ImagePath is where an image is stored (no existence check).
func (s *Store) ImagePath(name, file string) string {
	return filepath.Join(s.Dir(name), ImagesDir, file)
}

// Images lists a project's uploaded images, newest first.
func (s *Store) Images(name string) ([]Image, error) {
	d, err := s.dir(name)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(d, ImagesDir))
	if errors.Is(err, fs.ErrNotExist) {
		return []Image{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Image{}
	for _, e := range entries {
		if e.IsDir() || ValidImage(e.Name()) != nil {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Image{Name: e.Name(), Size: fi.Size(), Modified: fi.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// imageNames lists just the file names, for Info.
func imageNames(d string) []string {
	entries, err := os.ReadDir(filepath.Join(d, ImagesDir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && ValidImage(e.Name()) == nil {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// ReadImage returns the bytes of one uploaded image.
func (s *Store) ReadImage(name, file string) ([]byte, error) {
	if _, err := s.dir(name); err != nil {
		return nil, err
	}
	if err := ValidImage(file); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(s.ImagePath(name, file))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("image %s/%s: %w", name, file, ErrNotFound)
	}
	return b, err
}

// WriteImage stores an uploaded image, replacing one of the same name.
func (s *Store) WriteImage(name, file string, b []byte) (Image, error) {
	d, err := s.dir(name)
	if err != nil {
		return Image{}, err
	}
	if err := ValidImage(file); err != nil {
		return Image{}, err
	}
	if len(b) == 0 {
		return Image{}, errors.New("the image is empty")
	}
	if len(b) > MaxImageBytes {
		return Image{}, fmt.Errorf("image is %d MB, the limit is %d MB: %w",
			len(b)>>20, MaxImageBytes>>20, ErrTooLarge)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Join(d, ImagesDir), 0o755); err != nil {
		return Image{}, err
	}
	if err := writeAtomic(s.ImagePath(name, file), b); err != nil {
		return Image{}, err
	}
	return Image{Name: file, Size: int64(len(b)), Modified: time.Now()}, nil
}

// DeleteImage removes an uploaded image.
func (s *Store) DeleteImage(name, file string) error {
	if _, err := s.dir(name); err != nil {
		return err
	}
	if err := ValidImage(file); err != nil {
		return err
	}
	err := os.Remove(s.ImagePath(name, file))
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("image %s/%s: %w", name, file, ErrNotFound)
	}
	return err
}

// SafeImageName turns a name chosen by a browser or an agent into one ValidImage accepts:
// the extension decides the format and is lower-cased, and everything unusable in the stem
// becomes a dash.
func SafeImageName(s string) string {
	// Browsers mostly send a bare file name, but not always; treat both separators as one
	// so a path can never walk out of the images folder whatever the server runs on.
	s = path.Base(strings.ReplaceAll(s, `\`, "/"))
	ext := strings.ToLower(filepath.Ext(s))
	switch ext {
	case ".png", ".jpg", ".jpeg":
	default:
		ext = ".png"
	}
	stem := strings.TrimSuffix(s, filepath.Ext(s))
	var b strings.Builder
	for _, r := range stem {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	stem = strings.Trim(b.String(), "-._")
	if stem == "" {
		stem = "image"
	}
	if len(stem) > 60-len(ext) {
		stem = stem[:60-len(ext)]
	}
	return stem + ext
}

// UniqueImageName appends a number to name until it does not collide with an image the
// project already has, so a second drag of the same file does not quietly replace the
// first one.
func (s *Store) UniqueImageName(name, file string) string {
	file = SafeImageName(file)
	ext := filepath.Ext(file)
	stem := strings.TrimSuffix(file, ext)
	for n := 2; n < 1000; n++ {
		if _, err := os.Stat(s.ImagePath(name, file)); errors.Is(err, fs.ErrNotExist) {
			return file
		}
		file = fmt.Sprintf("%s-%d%s", stem, n, ext)
	}
	return file
}
