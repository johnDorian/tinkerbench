// Package stl reads STL files, ASCII or binary, into triangles in millimetres.
package stl

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/johnDorian/tinkerbench/orcaslicer/internal/geom"
)

// ErrNotSTL is returned for data that is neither a binary nor an ASCII STL.
var ErrNotSTL = errors.New("not an STL file")

// Parse reads an STL file. Binary is recognised by its exact length (84 + 50 per triangle),
// which also covers binary files whose header starts with "solid".
func Parse(data []byte) ([]geom.Triangle, error) {
	var tris []geom.Triangle
	var err error
	switch {
	case isBinary(data):
		tris, err = parseBinary(data)
	case isASCII(data):
		tris, err = parseASCII(data)
	case len(data) >= 84 && 84+50*uint64(binary.LittleEndian.Uint32(data[80:84])) <= uint64(len(data)):
		// binary with trailing bytes after the last triangle
		tris, err = parseBinary(data)
	default:
		return nil, ErrNotSTL
	}
	if err != nil {
		return nil, err
	}
	if len(tris) == 0 {
		return nil, errors.New("STL has no triangles")
	}
	for _, t := range tris {
		for _, v := range t {
			for _, c := range v {
				if math.IsNaN(c) || math.IsInf(c, 0) {
					return nil, errors.New("STL has non-finite coordinates")
				}
			}
		}
	}
	return tris, nil
}

func isBinary(data []byte) bool {
	if len(data) < 84 {
		return false
	}
	n := uint64(binary.LittleEndian.Uint32(data[80:84]))
	return uint64(len(data)) == 84+50*n
}

func isASCII(data []byte) bool {
	head := data[:min(len(data), 256)]
	return bytes.HasPrefix(bytes.TrimSpace(head), []byte("solid")) && bytes.Contains(data, []byte("facet"))
}

func parseBinary(data []byte) ([]geom.Triangle, error) {
	n := int(binary.LittleEndian.Uint32(data[80:84]))
	if 84+50*n > len(data) {
		return nil, errors.New("binary STL is truncated")
	}
	tris := make([]geom.Triangle, n)
	f := func(off int) float64 {
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(data[off:])))
	}
	for i := 0; i < n; i++ {
		off := 84 + 50*i + 12 // skip the normal
		for v := 0; v < 3; v++ {
			tris[i][v] = geom.Vec3{f(off), f(off + 4), f(off + 8)}
			off += 12
		}
	}
	return tris, nil
}

func parseASCII(data []byte) ([]geom.Triangle, error) {
	var tris []geom.Triangle
	var verts []geom.Vec3
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	line := 0
	for sc.Scan() {
		line++
		f := strings.Fields(sc.Text())
		if len(f) == 0 || f[0] != "vertex" {
			continue
		}
		if len(f) != 4 {
			return nil, fmt.Errorf("line %d: vertex needs 3 coordinates", line)
		}
		var v geom.Vec3
		for i := 0; i < 3; i++ {
			c, err := strconv.ParseFloat(f[i+1], 64)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", line, err)
			}
			v[i] = c
		}
		verts = append(verts, v)
		if len(verts) == 3 {
			tris = append(tris, geom.Triangle{verts[0], verts[1], verts[2]})
			verts = verts[:0]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(verts) != 0 {
		return nil, errors.New("STL ends in the middle of a facet")
	}
	return tris, nil
}
