package main

import (
	"fmt"
	"math"
)

type pt struct{ X, Y float64 }

func dist(a, b pt) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

func segDist(p, a, b pt) float64 {
	dx, dy := b.X-a.X, b.Y-a.Y
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return dist(p, a)
	}
	t := math.Max(0, math.Min(1, ((p.X-a.X)*dx+(p.Y-a.Y)*dy)/l2))
	return math.Hypot(p.X-(a.X+t*dx), p.Y-(a.Y+t*dy))
}

// fmtLen formats a pixel length using pixels-per-mm (0 = uncalibrated).
func fmtLen(px, ppm float64) string {
	if ppm <= 0 {
		return fmt.Sprintf("%.1f px", px)
	}
	mm := px / ppm
	if math.Abs(mm) < 1 {
		return fmt.Sprintf("%.1f µm", mm*1000)
	}
	return fmt.Sprintf("%.3f mm", mm)
}

func fmtArea(px2, ppm float64) string {
	if ppm <= 0 {
		return fmt.Sprintf("%.0f px²", px2)
	}
	mm2 := px2 / (ppm * ppm)
	if mm2 < 0.01 {
		return fmt.Sprintf("%.0f µm²", mm2*1e6)
	}
	return fmt.Sprintf("%.4f mm²", mm2)
}

// points needed to complete a shape (0 = open-ended polyline)
var need = map[string]int{"line": 2, "circle": 2, "rect": 2, "calibrate": 2, "angle": 3, "polyline": 0}

var typeName = map[string]string{"line": "Distance", "polyline": "Path", "angle": "Angle",
	"circle": "Circle", "rect": "Rectangle", "calibrate": "Calibration"}

type Shape struct {
	Kind string
	Pts  []pt
}

func (s *Shape) angleDeg() float64 {
	a, v, c := s.Pts[0], s.Pts[1], s.Pts[2]
	ux, uy := a.X-v.X, a.Y-v.Y
	wx, wy := c.X-v.X, c.Y-v.Y
	n := math.Hypot(ux, uy) * math.Hypot(wx, wy)
	if n == 0 {
		return 0
	}
	return math.Acos(math.Max(-1, math.Min(1, (ux*wx+uy*wy)/n))) * 180 / math.Pi
}

// Info returns the overlay label and a detail string.
func (s *Shape) Info(ppm float64) (string, string) {
	P := s.Pts
	switch s.Kind {
	case "calibrate":
		return fmt.Sprintf("%.2f px", dist(P[0], P[1])), ""
	case "line":
		L := dist(P[0], P[1])
		ang := math.Atan2(-(P[1].Y-P[0].Y), P[1].X-P[0].X) * 180 / math.Pi
		if math.Abs(ang) < 0.05 {
			ang = 0
		}
		return fmtLen(L, ppm), fmt.Sprintf("Δx %s, Δy %s, angle %.1f°",
			fmtLen(math.Abs(P[1].X-P[0].X), ppm), fmtLen(math.Abs(P[1].Y-P[0].Y), ppm), ang)
	case "polyline":
		L := 0.0
		for i := 0; i+1 < len(P); i++ {
			L += dist(P[i], P[i+1])
		}
		return fmtLen(L, ppm), fmt.Sprintf("%d segments", max(len(P)-1, 0))
	case "angle":
		if len(P) < 3 {
			return "", ""
		}
		return fmt.Sprintf("%.1f°", s.angleDeg()), ""
	case "circle":
		r := dist(P[0], P[1])
		return "Ø " + fmtLen(2*r, ppm), fmt.Sprintf("radius %s, area %s", fmtLen(r, ppm), fmtArea(math.Pi*r*r, ppm))
	case "rect":
		w, h := math.Abs(P[1].X-P[0].X), math.Abs(P[1].Y-P[0].Y)
		return fmtLen(w, ppm) + " × " + fmtLen(h, ppm),
			fmt.Sprintf("area %s, perimeter %s", fmtArea(w*h, ppm), fmtLen(2*(w+h), ppm))
	}
	return "", ""
}

func (s *Shape) degenerate() bool {
	P := s.Pts
	switch s.Kind {
	case "polyline":
		if len(P) < 2 {
			return true
		}
		for _, q := range P[1:] {
			if dist(P[0], q) >= 1e-6 {
				return false
			}
		}
		return true
	case "angle":
		return len(P) < 3 || dist(P[0], P[1]) < 1e-6 || dist(P[1], P[2]) < 1e-6
	}
	return dist(P[0], P[1]) < 1e-6
}

func niceLengthMM(target float64) float64 {
	if target <= 0 {
		return 1
	}
	e := math.Floor(math.Log10(target))
	for _, f := range []float64{1, 2, 5, 10} {
		if f*math.Pow(10, e) >= target*0.7 {
			return f * math.Pow(10, e)
		}
	}
	return math.Pow(10, e+1)
}
