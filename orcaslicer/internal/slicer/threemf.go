package slicer

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/geom"
)

// Object is one placed model in bed coordinates, ready for the 3MF.
type Object struct {
	Name string
	Tris []geom.Triangle
}

const contentTypes = `<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="model" ContentType="application/vnd.ms-package.3dmanufacturing-3dmodel+xml"/></Types>`

const rootRels = `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Target="/3D/3dmodel.model" Id="rel0" Type="http://schemas.microsoft.com/3dmanufacturing/2013/01/3dmodel"/></Relationships>`

// WriteThreeMF writes a 3MF (zip with 3D/3dmodel.model) that holds the objects at their bed
// positions. The vertices are already in bed coordinates and the build items have no transform,
// so OrcaSlicer reads the positions as they are. The same file is the plate's 3MF download.
func WriteThreeMF(path string, objects []Object) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, part := range []struct{ name, body string }{{"[Content_Types].xml", contentTypes}, {"_rels/.rels", rootRels}} {
		w, err := zw.Create(part.name)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, part.body); err != nil {
			return err
		}
	}
	w, err := zw.Create("3D/3dmodel.model")
	if err != nil {
		return err
	}
	bw := bufio.NewWriter(w)
	fmt.Fprint(bw, `<?xml version="1.0" encoding="UTF-8"?>
<model unit="millimeter" xml:lang="en-US" xmlns="http://schemas.microsoft.com/3dmanufacturing/core/2015/02"><resources>`)
	for i, o := range objects {
		writeObject(bw, i+1, o)
	}
	fmt.Fprint(bw, `</resources><build>`)
	for i := range objects {
		fmt.Fprintf(bw, `<item objectid="%d"/>`, i+1)
	}
	fmt.Fprint(bw, `</build></model>`)
	if err := bw.Flush(); err != nil {
		return err
	}
	return zw.Close()
}

func writeObject(w *bufio.Writer, id int, o Object) {
	// one vertex per distinct point; triangles index into them
	index := map[geom.Vec3]int{}
	var verts []geom.Vec3
	tris := make([][3]int, 0, len(o.Tris))
	for _, t := range o.Tris {
		var idx [3]int
		for k, v := range t {
			i, ok := index[v]
			if !ok {
				i = len(verts)
				index[v] = i
				verts = append(verts, v)
			}
			idx[k] = i
		}
		tris = append(tris, idx)
	}
	fmt.Fprintf(w, `<object id="%d" type="model" name="%s"><mesh><vertices>`, id, xmlEscape(o.Name))
	for _, v := range verts {
		fmt.Fprintf(w, `<vertex x="%s" y="%s" z="%s"/>`, num(v[0]), num(v[1]), num(v[2]))
	}
	fmt.Fprint(w, `</vertices><triangles>`)
	for _, t := range tris {
		fmt.Fprintf(w, `<triangle v1="%d" v2="%d" v3="%d"/>`, t[0], t[1], t[2])
	}
	fmt.Fprint(w, `</triangles></mesh></object>`)
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', 5, 64) }

func xmlEscape(s string) string {
	var b []byte
	for _, r := range s {
		switch r {
		case '&':
			b = append(b, "&amp;"...)
		case '<':
			b = append(b, "&lt;"...)
		case '>':
			b = append(b, "&gt;"...)
		case '"':
			b = append(b, "&quot;"...)
		default:
			b = append(b, string(r)...)
		}
	}
	return string(b)
}
