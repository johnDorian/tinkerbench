package measure

import "math"

// pitchByFFT returns the dominant period of a 1D profile, in samples, or 0 when there is
// no usable peak.
//
// It is band-limited to periods between minPx and a quarter of the profile, which is what
// makes it robust here: the part covers a stretch of the grid and puts a broad envelope on
// the profile, but that envelope lives at a period comparable to the whole image and falls
// outside the band. The doubled edges Canny finds on each printed line show up as
// harmonics, which are weaker than the fundamental.
//
// Only the bins inside the band are evaluated (a direct transform, not a full FFT), which
// is both cheaper than transforming everything and all that is needed.
func pitchByFFT(profile []float64, minPx float64) float64 {
	n := len(profile)
	if n < 32 {
		return 0
	}
	var mean float64
	nonzero := false
	for _, v := range profile {
		mean += v
		if v != 0 {
			nonzero = true
		}
	}
	if !nonzero {
		return 0
	}
	mean /= float64(n)

	maxPx := math.Min(200, float64(n)/4)
	// frequency k/n must lie strictly inside (1/maxPx, 1/minPx)
	lo := int(math.Floor(float64(n)/maxPx)) + 1
	hi := int(math.Ceil(float64(n)/minPx)) - 1
	lo, hi = max(lo, 1), min(hi, n/2)
	if lo > hi {
		return 0
	}

	// |X_k| for k in the band, plus one bin either side for the interpolation below
	kLo, kHi := max(lo-1, 1), min(hi+1, n/2)
	spec := make([]float64, kHi-kLo+1)
	tw := twiddles(n)
	for k := kLo; k <= kHi; k++ {
		var re, im float64
		idx := 0
		for t := range n {
			c, s := tw[idx*2], tw[idx*2+1]
			v := profile[t] - mean
			re += v * c
			im -= v * s
			idx += k
			if idx >= n {
				idx -= n
			}
		}
		spec[k-kLo] = math.Hypot(re, im)
	}

	best, bestK := -1.0, -1
	for k := lo; k <= hi; k++ {
		if v := spec[k-kLo]; v > best {
			best, bestK = v, k
		}
	}
	if bestK < 0 || best <= 0 {
		return 0
	}

	// parabolic interpolation on the spectral peak, for sub-sample accuracy
	shift := 0.0
	if bestK-1 >= kLo && bestK+1 <= kHi {
		y0, y1, y2 := spec[bestK-1-kLo], spec[bestK-kLo], spec[bestK+1-kLo]
		if d := y0 - 2*y1 + y2; d != 0 {
			shift = 0.5 * (y0 - y2) / d
		}
	}
	f := (float64(bestK) + shift) / float64(n)
	if f <= 0 {
		return 0
	}
	return 1 / f
}

// twiddles tabulates cos and sin of -2*pi*m/n for m = 0..n-1, interleaved, so the
// transform above needs no trigonometry in its inner loop.
func twiddles(n int) []float64 {
	t := make([]float64, 2*n)
	for m := range n {
		a := 2 * math.Pi * float64(m) / float64(n)
		t[m*2], t[m*2+1] = math.Cos(a), math.Sin(a)
	}
	return t
}
