package slicer

import (
	"archive/zip"
	"encoding/xml"
	"io"
	"path/filepath"
	"testing"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/geom"
)

type xmlModel struct {
	Resources struct {
		Objects []struct {
			ID   int    `xml:"id,attr"`
			Name string `xml:"name,attr"`
			Mesh struct {
				Vertices []struct {
					X float64 `xml:"x,attr"`
					Y float64 `xml:"y,attr"`
					Z float64 `xml:"z,attr"`
				} `xml:"vertices>vertex"`
				Triangles []struct {
					V1 int `xml:"v1,attr"`
					V2 int `xml:"v2,attr"`
					V3 int `xml:"v3,attr"`
				} `xml:"triangles>triangle"`
			} `xml:"mesh"`
		} `xml:"object"`
	} `xml:"resources"`
	Build struct {
		Items []struct {
			ObjectID int `xml:"objectid,attr"`
		} `xml:"item"`
	} `xml:"build"`
}

func TestWriteThreeMFRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plate.3mf")
	cube := []geom.Triangle{
		{{10, 10, 0}, {20, 10, 0}, {20, 20, 0}},
		{{10, 10, 0}, {20, 20, 0}, {10, 20, 0}},
	}
	if err := WriteThreeMF(path, []Object{{Name: "a & b", Tris: cube}}); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	var model []byte
	for _, f := range zr.File {
		if f.Name == "3D/3dmodel.model" {
			rc, _ := f.Open()
			model, _ = io.ReadAll(rc)
			rc.Close()
		}
	}
	var m xmlModel
	if err := xml.Unmarshal(model, &m); err != nil {
		t.Fatalf("model xml: %v", err)
	}
	if len(m.Resources.Objects) != 1 {
		t.Fatalf("objects = %d", len(m.Resources.Objects))
	}
	o := m.Resources.Objects[0]
	if o.Name != "a & b" || len(o.Mesh.Vertices) != 4 || len(o.Mesh.Triangles) != 2 {
		t.Fatalf("object = %+v", o)
	}
	if o.Mesh.Vertices[0].X != 10 || o.Mesh.Vertices[2].Y != 20 {
		t.Fatalf("vertices not in bed coordinates: %+v", o.Mesh.Vertices)
	}
	if len(m.Build.Items) != 1 || m.Build.Items[0].ObjectID != 1 {
		t.Fatalf("build = %+v", m.Build)
	}
}
