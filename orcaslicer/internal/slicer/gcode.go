package slicer

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Stats are the numbers OrcaSlicer writes into the G-code header and footer.
type Stats struct {
	Seconds    int
	FilamentMM float64
	FilamentG  float64
	Layers     int
	MaxZ       float64
}

var durationPart = regexp.MustCompile(`(\d+)\s*([dhms])`)

// ParseGcode reads the stats OrcaSlicer writes as comments: the estimated time, the filament used,
// the layer count and the maximum height.
func ParseGcode(r io.Reader) (Stats, error) {
	var st Stats
	found := 0
	layersFallback := 0
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, ";") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		switch {
		case ok && strings.HasPrefix(key, "; estimated printing time (normal mode)"):
			st.Seconds = parseDuration(val)
			found++
		case ok && strings.HasPrefix(key, "; filament used [mm]"):
			st.FilamentMM = parseFloat(val)
			found++
		case ok && strings.HasPrefix(key, "; total filament used [g]"):
			st.FilamentG = parseFloat(val)
			found++
		case ok && strings.HasPrefix(key, "; total layers count"):
			st.Layers = int(parseFloat(val))
			found++
		case strings.HasPrefix(line, "; total layer number:"):
			layersFallback = int(parseFloat(strings.TrimPrefix(line, "; total layer number:")))
		case strings.HasPrefix(line, "; max_z_height:"):
			st.MaxZ = parseFloat(strings.TrimPrefix(line, "; max_z_height:"))
		}
	}
	if err := sc.Err(); err != nil {
		return Stats{}, err
	}
	if st.Layers == 0 {
		st.Layers = layersFallback
	}
	if found < 3 {
		return Stats{}, fmt.Errorf("G-code has no OrcaSlicer statistics footer")
	}
	return st, nil
}

// parseDuration reads "1h 2m 3s", "44m45s" or "21m 57s" into seconds.
func parseDuration(s string) int {
	total := 0
	for _, m := range durationPart.FindAllStringSubmatch(s, -1) {
		n, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "d":
			total += n * 86400
		case "h":
			total += n * 3600
		case "m":
			total += n * 60
		case "s":
			total += n
		}
	}
	return total
}

func parseFloat(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

// DurationLabel formats seconds as "1h02m03s" or "44m45s", as used in the G-code file name.
func DurationLabel(sec int) string {
	h, m, s := sec/3600, (sec%3600)/60, sec%60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm%02ds", m, s)
}
