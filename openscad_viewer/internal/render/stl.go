package render

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"strconv"
	"strings"
)

// Stats describe a mesh in millimetres.
type Stats struct {
	Triangles int        `json:"triangles"`
	Min       [3]float64 `json:"min"`
	Max       [3]float64 `json:"max"`
	Size      [3]float64 `json:"size"`
	VolumeMM3 float64    `json:"volume_mm3"` // only meaningful for a closed mesh
}

// Triangle is three vertices in millimetres.
type Triangle [3][3]float64

// ReadSTLFile computes Stats for a binary or ASCII STL file.
func ReadSTLFile(path string) (Stats, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Stats{}, err
	}
	return ReadSTL(b)
}

// ReadSTL computes Stats for binary or ASCII STL data.
func ReadSTL(b []byte) (Stats, error) {
	tris, err := ReadTriangles(b)
	if err != nil {
		return Stats{}, err
	}
	s := Stats{Triangles: len(tris)}
	if len(tris) == 0 {
		return s, nil
	}
	s.Min = tris[0][0]
	s.Max = tris[0][0]
	var vol float64
	for _, t := range tris {
		for _, v := range t {
			for c := range 3 {
				s.Min[c] = math.Min(s.Min[c], v[c])
				s.Max[c] = math.Max(s.Max[c], v[c])
			}
		}
		a, b, c := t[0], t[1], t[2]
		// signed volume of the tetrahedron (origin, a, b, c)
		vol += a[0]*(b[1]*c[2]-b[2]*c[1]) - a[1]*(b[0]*c[2]-b[2]*c[0]) + a[2]*(b[0]*c[1]-b[1]*c[0])
	}
	for c := range 3 {
		s.Size[c] = round(s.Max[c] - s.Min[c])
		s.Min[c], s.Max[c] = round(s.Min[c]), round(s.Max[c])
	}
	s.VolumeMM3 = round(math.Abs(vol) / 6)
	return s, nil
}

// ReadTrianglesFile returns the triangles of a binary or ASCII STL file.
func ReadTrianglesFile(path string) ([]Triangle, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ReadTriangles(b)
}

// ReadTriangles returns the triangles of binary or ASCII STL data.
func ReadTriangles(b []byte) ([]Triangle, error) {
	if len(b) >= 84 && 84+50*int(binary.LittleEndian.Uint32(b[80:84])) == len(b) {
		n := int(binary.LittleEndian.Uint32(b[80:84]))
		tris := make([]Triangle, n)
		for i := range n {
			off := 84 + 50*i + 12 // skip the normal
			for v := range 3 {
				for c := range 3 {
					bits := binary.LittleEndian.Uint32(b[off+12*v+4*c:])
					tris[i][v][c] = float64(math.Float32frombits(bits))
				}
			}
		}
		return tris, nil
	}
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte("solid")) {
		var tris []Triangle
		var cur Triangle
		k := 0
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) != 4 || f[0] != "vertex" {
				continue
			}
			for c := range 3 {
				x, err := strconv.ParseFloat(f[c+1], 64)
				if err != nil {
					return nil, err
				}
				cur[k][c] = x
			}
			if k++; k == 3 {
				tris = append(tris, cur)
				k = 0
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
		return tris, nil
	}
	return nil, errors.New("not an STL file")
}

func round(x float64) float64 { return math.Round(x*1000) / 1000 }
