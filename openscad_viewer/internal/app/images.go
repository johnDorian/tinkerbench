package app

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"os"

	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/events"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/imaging"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/measure"
	"github.com/johnDorian/tinkerbench/openscad_viewer/internal/project"
)

// Images lists a project's uploaded images, with their size in pixels and the resolution
// recorded in the file (0 when it records none).
func (a *App) Images(name string) ([]project.Image, error) {
	list, err := a.Store.Images(name)
	if err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Width, list[i].Height, list[i].DPI = describe(a.Store.ImagePath(name, list[i].Name))
	}
	return list, nil
}

// describe reads just the header of an image file for its size and resolution, so listing
// a project does not have to decode every scan in it.
func describe(path string) (w, h int, dpi float64) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, 0
	}
	defer f.Close()
	const header = 1 << 18
	if cfg, _, err := image.DecodeConfig(io.LimitReader(f, header)); err == nil {
		w, h = cfg.Width, cfg.Height
	}
	if _, err := f.Seek(0, io.SeekStart); err == nil {
		dpi, _ = imaging.DPI(f)
	}
	return w, h, dpi
}

// UploadImage stores a scan or photo with a project. The bytes have to decode as a PNG or
// JPEG: a file that cannot be read is worse than no file, because it only fails later when
// someone tries to measure it.
func (a *App) UploadImage(name, file string, data []byte, by string) (project.Image, error) {
	img, err := imaging.Decode(data)
	if err != nil {
		return project.Image{}, fmt.Errorf("not a readable PNG or JPEG: %w", err)
	}
	stored, err := a.Store.WriteImage(name, file, data)
	if err != nil {
		return project.Image{}, err
	}
	stored.Width, stored.Height = img.W, img.H
	stored.DPI, _ = imaging.DPI(bytes.NewReader(data))
	a.imageEvent(name, by, "added "+file)
	return stored, nil
}

func (a *App) DeleteImage(name, file, by string) error {
	if err := a.Store.DeleteImage(name, file); err != nil {
		return err
	}
	a.imageEvent(name, by, "deleted "+file)
	return nil
}

func (a *App) imageEvent(name, by, detail string) {
	a.Hub.Publish(events.Event{Kind: events.ImagesChanged, Project: name, Source: by, Detail: detail})
}

// Measure measures the part in one of a project's images. The result is not stored: it is
// cheap to redo and the options that produced it matter as much as the numbers, so it
// belongs with whoever asked for it.
func (a *App) Measure(name, file string, o measure.Options) (*measure.Result, error) {
	if file == "" {
		list, err := a.Store.Images(name)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, errors.New("the project has no images; upload a scan of the part first")
		}
		file = list[0].Name // the most recent upload
	}
	if _, err := a.Store.ReadImage(name, file); err != nil {
		return nil, err
	}
	img, dpi, err := imaging.Load(a.Store.ImagePath(name, file))
	if err != nil {
		return nil, err
	}
	res, err := measure.Measure(img, dpi, o)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return res, nil
}

// ViewImage returns a PNG of an uploaded image, shrunk to fit maxPx on its long edge so it
// is worth sending to an agent: a 1200 dpi scan is tens of megabytes and nothing useful is
// lost by looking at a smaller copy of it.
func (a *App) ViewImage(name, file string, maxPx int) ([]byte, int, int, error) {
	if _, err := a.Store.ReadImage(name, file); err != nil {
		return nil, 0, 0, err
	}
	img, _, err := imaging.Load(a.Store.ImagePath(name, file))
	if err != nil {
		return nil, 0, 0, err
	}
	if maxPx <= 0 {
		maxPx = 1000
	}
	small, _ := imaging.Scale(img, min(maxPx, 3000))
	png, err := imaging.EncodePNG(small)
	return png, small.W, small.H, err
}
