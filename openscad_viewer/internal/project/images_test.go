package project

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImageStore(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("tag", "", "", "test"); err != nil {
		t.Fatal(err)
	}

	if list, err := s.Images("tag"); err != nil || len(list) != 0 {
		t.Fatalf("a new project should have no images, got %v %v", list, err)
	}

	data := pngBytes(t, 8, 8)
	img, err := s.WriteImage("tag", "scan.png", data)
	if err != nil {
		t.Fatal(err)
	}
	if img.Name != "scan.png" || img.Size != int64(len(data)) {
		t.Errorf("stored %+v", img)
	}

	got, err := s.ReadImage("tag", "scan.png")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read back %d bytes, %v", len(got), err)
	}

	// Images show up on the project, and are kept apart from the .scad sources.
	info, err := s.Get("tag")
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Images) != 1 || info.Images[0] != "scan.png" {
		t.Errorf("project images = %v", info.Images)
	}
	for _, f := range info.Files {
		if strings.HasSuffix(f, ".png") {
			t.Errorf("an image leaked into the source file list: %v", info.Files)
		}
	}

	if err := s.DeleteImage("tag", "scan.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReadImage("tag", "scan.png"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after deleting, read gave %v, want ErrNotFound", err)
	}
}

func TestImageNameRules(t *testing.T) {
	for _, ok := range []string{"scan.png", "a.jpg", "a.jpeg", "IMG_0001.JPG", "part-2.v1.png"} {
		if err := ValidImage(ok); err != nil {
			t.Errorf("%q should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "scan", "scan.gif", "scan.scad", "../escape.png", "a/b.png", ".hidden.png", "scan.png.tmp"} {
		if err := ValidImage(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestSafeImageName(t *testing.T) {
	cases := map[string]string{
		"scan.png":                   "scan.png",
		"IMG 0001 (1).JPG":           "IMG-0001--1.jpg",
		"/tmp/../etc/passwd":         "passwd.png",
		"c:\\photos\\part scan.jpeg": "part-scan.jpeg",
		"....png":                    "image.png",
		"":                           "image.png",
	}
	for in, want := range cases {
		if got := SafeImageName(in); got != want {
			t.Errorf("SafeImageName(%q) = %q, want %q", in, got, want)
		}
		if err := ValidImage(SafeImageName(in)); err != nil {
			t.Errorf("SafeImageName(%q) produced an invalid name: %v", in, err)
		}
	}
	long := strings.Repeat("x", 200) + ".png"
	if err := ValidImage(SafeImageName(long)); err != nil {
		t.Errorf("a very long name was not shortened enough: %v", err)
	}
}

func TestUniqueImageName(t *testing.T) {
	s, _ := Open(t.TempDir())
	s.Create("tag", "", "", "test")
	if got := s.UniqueImageName("tag", "scan.png"); got != "scan.png" {
		t.Errorf("first upload got %q", got)
	}
	s.WriteImage("tag", "scan.png", pngBytes(t, 4, 4))
	if got := s.UniqueImageName("tag", "scan.png"); got != "scan-2.png" {
		t.Errorf("second upload of the same name got %q, want scan-2.png", got)
	}
}

func TestWriteImageLimits(t *testing.T) {
	s, _ := Open(t.TempDir())
	s.Create("tag", "", "", "test")
	if _, err := s.WriteImage("tag", "scan.png", nil); err == nil {
		t.Error("an empty upload should be refused")
	}
	if _, err := s.WriteImage("tag", "scan.png", make([]byte, MaxImageBytes+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("an oversized upload gave %v, want ErrTooLarge", err)
	}
	if _, err := s.WriteImage("nope", "scan.png", pngBytes(t, 4, 4)); !errors.Is(err, ErrNotFound) {
		t.Errorf("writing to a missing project gave %v", err)
	}
}

func TestContentType(t *testing.T) {
	for file, want := range map[string]string{"a.png": "image/png", "a.PNG": "image/png", "a.jpg": "image/jpeg", "a.jpeg": "image/jpeg"} {
		if got := ContentType(file); got != want {
			t.Errorf("ContentType(%q) = %q, want %q", file, got, want)
		}
	}
}
