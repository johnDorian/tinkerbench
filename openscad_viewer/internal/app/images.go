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

// Images lists a project's uploaded images, with their size in pixels (upright, as a photo
// viewer shows them), the resolution recorded in the file and whether that is a scale.
func (a *App) Images(name string) ([]project.Image, error) {
	list, err := a.Store.Images(name)
	if err != nil {
		return nil, err
	}
	for i := range list {
		w, h, meta, _ := describe(a.Store.ImagePath(name, list[i].Name))
		fillInfo(&list[i], w, h, meta)
	}
	return list, nil
}

// fillInfo copies what was read from a file onto its listing.
func fillInfo(img *project.Image, w, h int, meta imaging.Meta) {
	img.Width, img.Height, img.DPI = w, h, meta.DPI
	img.Camera = meta.CameraName()
	img.DPIUsable = meta.DPIUsable()
}

// describe reads just the header of an image file for its size and metadata, so listing a
// project does not have to decode every scan in it. The size is swapped for a photo turned
// on its side, to match what Decode gives.
func describe(path string) (w, h int, meta imaging.Meta, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, imaging.Meta{}, err
	}
	defer f.Close()
	head, err := io.ReadAll(io.LimitReader(f, 1<<18))
	if err != nil {
		return 0, 0, imaging.Meta{}, err
	}
	meta = imaging.HeadMeta(head)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(head))
	if err != nil {
		return 0, 0, meta, err
	}
	w, h = cfg.Width, cfg.Height
	if meta.Orientation >= 5 && meta.Orientation <= 8 {
		w, h = h, w
	}
	return w, h, meta, nil
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
	meta, _ := imaging.ReadMeta(bytes.NewReader(data))
	fillInfo(&stored, img.W, img.H, meta)
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
	img, meta, err := imaging.Load(a.Store.ImagePath(name, file))
	if err != nil {
		return nil, err
	}
	res, err := measure.Measure(img, meta, o)
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
