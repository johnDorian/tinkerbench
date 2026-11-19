package imaging

import (
	"math"
	"sort"
)

// Line is one line from the Hough transform, in the usual normal form:
// x*cos(Theta) + y*sin(Theta) = Rho.
type Line struct {
	Rho   float64
	Theta float64 // radians, in [0, pi)
	Votes int
}

// HoughLines is the standard Hough line transform over the set pixels of an edge map,
// matching OpenCV's HoughLines(edges, rho, theta, threshold): it keeps accumulator cells
// above threshold that are also a local maximum, strongest first.
//
// rhoStep is in pixels and thetaStep in radians.
func HoughLines(edges *Gray, rhoStep, thetaStep float64, threshold, maxLines int) []Line {
	if rhoStep <= 0 || thetaStep <= 0 {
		return nil
	}
	numAngle := int(math.Round(math.Pi / thetaStep))
	numRho := int(math.Round((float64(edges.W+edges.H)*2 + 1) / rhoStep))
	if numAngle < 1 || numRho < 1 {
		return nil
	}
	sin := make([]float64, numAngle)
	cos := make([]float64, numAngle)
	for n := range numAngle {
		a := float64(n) * thetaStep
		sin[n], cos[n] = math.Sin(a)/rhoStep, math.Cos(a)/rhoStep
	}

	acc := make([]int32, (numAngle+2)*(numRho+2)) // one cell of padding, so maxima need no bounds checks
	stride := numRho + 2
	half := (numRho - 1) / 2
	for y := range edges.H {
		row := edges.Pix[y*edges.W : (y+1)*edges.W]
		fy := float64(y)
		for x, v := range row {
			if v == 0 {
				continue
			}
			fx := float64(x)
			for n := range numAngle {
				r := int(math.Round(fx*cos[n]+fy*sin[n])) + half
				if r >= 0 && r < numRho {
					acc[(n+1)*stride+r+1]++
				}
			}
		}
	}

	var out []Line
	for n := range numAngle {
		for r := range numRho {
			i := (n+1)*stride + r + 1
			v := acc[i]
			if int(v) > threshold && v > acc[i-1] && v >= acc[i+1] && v > acc[i-stride] && v >= acc[i+stride] {
				out = append(out, Line{
					Rho:   float64(r-half) * rhoStep,
					Theta: float64(n) * thetaStep,
					Votes: int(v),
				})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Votes > out[j].Votes })
	if maxLines > 0 && len(out) > maxLines {
		out = out[:maxLines]
	}
	return out
}
