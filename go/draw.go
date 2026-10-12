package main

import (
	"fmt"
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	colShape  = rgb(0x00, 0xe6, 0x76)
	colSel    = rgb(0xff, 0xeb, 0x3b)
	colActive = rgb(0x40, 0xc4, 0xff)
	colCal    = rgb(0xff, 0x98, 0x00)
	colCross  = rgb(0xff, 0x17, 0x44)
	colBlack  = rgb(0, 0, 0)
	colWhite  = rgb(255, 255, 255)
	colLabel  = rgb(0x10, 0x10, 0x10)
)

// gfx draws overlay primitives with GDI onto a DC.
type gfx struct {
	hdc   uintptr
	ui    float64
	fonts map[int]uintptr
}

func newGfx(hdc uintptr, ui float64) *gfx { return &gfx{hdc: hdc, ui: ui, fonts: map[int]uintptr{}} }

func (g *gfx) close() {
	for _, f := range g.fonts {
		pDeleteObject.Call(f)
	}
}

func ir(f float64) uintptr { return uintptr(int32(math.Round(f))) }

func (g *gfx) withPen(col uint32, w int, fn func()) {
	pen, _, _ := pCreatePen.Call(psSolid, uintptr(w), uintptr(col))
	old, _, _ := pSelectObject.Call(g.hdc, pen)
	fn()
	pSelectObject.Call(g.hdc, old)
	pDeleteObject.Call(pen)
}

func (g *gfx) polyline(pts []pt) {
	ps := make([]point, len(pts))
	for i, p := range pts {
		ps[i] = point{int32(math.Round(p.X)), int32(math.Round(p.Y))}
	}
	pPolyline.Call(g.hdc, uintptr(unsafe.Pointer(&ps[0])), uintptr(len(ps)))
}

func (g *gfx) line(pts []pt, col uint32, w int, halo bool) {
	if len(pts) < 2 {
		return
	}
	if halo {
		g.withPen(colBlack, w+2, func() { g.polyline(pts) })
	}
	g.withPen(col, w, func() { g.polyline(pts) })
}

func (g *gfx) hollow(fn func()) {
	nb, _, _ := pGetStockObject.Call(nullBrush)
	old, _, _ := pSelectObject.Call(g.hdc, nb)
	fn()
	pSelectObject.Call(g.hdc, old)
}

func (g *gfx) oval(cx, cy, r float64, col uint32, w int) {
	g.hollow(func() {
		for _, s := range []struct {
			c uint32
			w int
		}{{colBlack, w + 2}, {col, w}} {
			g.withPen(s.c, s.w, func() { pEllipse.Call(g.hdc, ir(cx-r), ir(cy-r), ir(cx+r), ir(cy+r)) })
		}
	})
}

func (g *gfx) rect(x0, y0, x1, y1 float64, col uint32, w int, fill bool) {
	l, t, r, b := math.Min(x0, x1), math.Min(y0, y1), math.Max(x0, x1), math.Max(y0, y1)
	if fill {
		br, _, _ := pCreateSolidBrush.Call(uintptr(colWhite))
		old, _, _ := pSelectObject.Call(g.hdc, br)
		g.withPen(col, w, func() { pRectangle.Call(g.hdc, ir(l), ir(t), ir(r)+1, ir(b)+1) })
		pSelectObject.Call(g.hdc, old)
		pDeleteObject.Call(br)
		return
	}
	g.hollow(func() {
		g.withPen(colBlack, w+2, func() { pRectangle.Call(g.hdc, ir(l), ir(t), ir(r)+1, ir(b)+1) })
		g.withPen(col, w, func() { pRectangle.Call(g.hdc, ir(l), ir(t), ir(r)+1, ir(b)+1) })
	})
}

// arc draws a counter-clockwise (screen) arc from angle start sweeping extent degrees.
func (g *gfx) arc(cx, cy, r, start, extent float64, col uint32, w int) {
	lo, hi := math.Min(start, start+extent), math.Max(start, start+extent)
	rad := math.Pi / 180
	sx, sy := cx+r*math.Cos(lo*rad), cy-r*math.Sin(lo*rad)
	ex, ey := cx+r*math.Cos(hi*rad), cy-r*math.Sin(hi*rad)
	for _, s := range []struct {
		c uint32
		w int
	}{{colBlack, w + 2}, {col, w}} {
		g.withPen(s.c, s.w, func() {
			pArc.Call(g.hdc, ir(cx-r), ir(cy-r), ir(cx+r), ir(cy+r), ir(sx), ir(sy), ir(ex), ir(ey))
		})
	}
}

func (g *gfx) font(px int) uintptr {
	if f, ok := g.fonts[px]; ok {
		return f
	}
	const fwBold, antialiased = 700, 4
	f, _, _ := pCreateFontW.Call(uintptr(uint32(-int32(px))), 0, 0, 0, fwBold, 0, 0, 0, 1, 0, 0, antialiased, 0,
		uintptr(unsafe.Pointer(u16("Segoe UI"))))
	g.fonts[px] = f
	return f
}

func (g *gfx) textSize(s string, px int) (int, int) {
	old, _, _ := pSelectObject.Call(g.hdc, g.font(px))
	u := windows.StringToUTF16(s)
	var sz size
	pGetTextExtentPoint32W.Call(g.hdc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&sz)))
	pSelectObject.Call(g.hdc, old)
	return int(sz.CX), int(sz.CY)
}

// text draws s with an opaque dark box; anchor top-left, or bottom-left when above is true.
func (g *gfx) text(x, y float64, s string, col uint32, sizePx float64, above bool, bg bool) {
	px := max(8, int(math.Round(sizePx)))
	tw, th := g.textSize(s, px)
	if above {
		y -= float64(th)
	}
	pad := 3 * g.ui
	old, _, _ := pSelectObject.Call(g.hdc, g.font(px))
	pSetTextColor.Call(g.hdc, uintptr(col))
	u := windows.StringToUTF16(s)
	flags := uintptr(0)
	var rc rect
	if bg {
		pSetBkMode.Call(g.hdc, bkOpaque)
		pSetBkColor.Call(g.hdc, uintptr(colLabel))
		rc = rect{int32(x - pad), int32(y - pad), int32(x + float64(tw) + pad), int32(y + float64(th) + pad)}
		flags = etoOpaque
	} else {
		pSetBkMode.Call(g.hdc, bkTransparent)
	}
	pExtTextOutW.Call(g.hdc, ir(x), ir(y), flags, uintptr(unsafe.Pointer(&rc)), uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), 0)
	pSelectObject.Call(g.hdc, old)
}

func drawShape(g *gfx, sh *Shape, m func(pt) pt, ppm float64, col uint32, labels, handles bool) {
	ui := g.ui
	pts := make([]pt, len(sh.Pts))
	for i, p := range sh.Pts {
		pts[i] = m(p)
	}
	w := max(1, int(math.Round(2*ui)))
	cross := func(q pt) {
		g.line([]pt{{q.X - 4*ui, q.Y}, {q.X + 4*ui, q.Y}}, col, w, true)
		g.line([]pt{{q.X, q.Y - 4*ui}, {q.X, q.Y + 4*ui}}, col, w, true)
	}
	var anchor *pt
	switch sh.Kind {
	case "line", "calibrate":
		a, b := pts[0], pts[1]
		g.line([]pt{a, b}, col, w, true)
		if L := dist(a, b); L > 0 {
			nx, ny, t := -(b.Y-a.Y)/L, (b.X-a.X)/L, 7*ui
			for _, q := range []pt{a, b} {
				g.line([]pt{{q.X - nx*t, q.Y - ny*t}, {q.X + nx*t, q.Y + ny*t}}, col, w, true)
			}
		}
		anchor = &pt{(a.X + b.X) / 2, (a.Y + b.Y) / 2}
	case "polyline":
		g.line(pts, col, w, true)
		for _, q := range pts {
			cross(q)
		}
		anchor = &pts[len(pts)-1]
	case "angle":
		if len(pts) >= 2 {
			g.line(pts[:2], col, w, true)
		}
		if len(pts) >= 3 {
			g.line(pts[1:3], col, w, true)
			v, a, c := pts[1], pts[0], pts[2]
			a1 := math.Atan2(-(a.Y-v.Y), a.X-v.X) * 180 / math.Pi
			a2 := math.Atan2(-(c.Y-v.Y), c.X-v.X) * 180 / math.Pi
			ext := math.Mod(math.Mod(a2-a1+180, 360)+360, 360) - 180
			r := math.Min(24*ui, 0.5*math.Min(dist(a, v), dist(c, v)))
			if r > 2 && math.Abs(ext) > 0.01 {
				g.arc(v.X, v.Y, r, a1, ext, col, w)
			}
		}
		if len(pts) >= 2 {
			anchor = &pts[1]
		} else {
			anchor = &pts[0]
		}
	case "circle":
		c, e := pts[0], pts[1]
		r := dist(c, e)
		g.oval(c.X, c.Y, r, col, w)
		g.line([]pt{c, e}, col, 1, false)
		cross(c)
		anchor = &pt{c.X, c.Y + r}
	case "rect":
		a, b := pts[0], pts[1]
		g.rect(a.X, a.Y, b.X, b.Y, col, w, false)
		anchor = &pt{math.Max(a.X, b.X), math.Max(a.Y, b.Y)}
	}
	if handles {
		h := 5 * ui
		for _, q := range pts {
			g.rect(q.X-h, q.Y-h, q.X+h, q.Y+h, col, max(1, int(ui)), true)
		}
	}
	if labels && anchor != nil {
		if t, _ := sh.Info(ppm); t != "" {
			g.text(anchor.X+8*ui, anchor.Y+8*ui, t, col, 13*ui, false, true)
		}
	}
}

func drawScalebar(g *gfx, W, H int, ppmScreen float64) {
	if ppmScreen <= 0 {
		return
	}
	ui := g.ui
	mm := niceLengthMM(float64(W) * 0.2 / ppmScreen)
	L := mm * ppmScreen
	x0, y := 20*ui, float64(H)-28*ui
	w := max(2, int(math.Round(3*ui)))
	g.line([]pt{{x0, y}, {x0 + L, y}}, colWhite, w, true)
	for _, x := range []float64{x0, x0 + L} {
		g.line([]pt{{x, y - 6*ui}, {x, y + 6*ui}}, colWhite, w, true)
	}
	label := fmt.Sprintf("%.6g mm", mm)
	if mm < 1 {
		label = fmt.Sprintf("%.6g µm", mm*1000)
	}
	g.text(x0, y-12*ui, label, colWhite, 13*ui, true, true)
}

func drawCrosshair(g *gfx, c pt, W, H int) {
	w := max(1, int(math.Round(g.ui)))
	g.line([]pt{{0, c.Y}, {float64(W), c.Y}}, colCross, w, true)
	g.line([]pt{{c.X, 0}, {c.X, float64(H)}}, colCross, w, true)
	r := 12 * g.ui
	g.hollow(func() {
		g.withPen(colCross, w, func() { pEllipse.Call(g.hdc, ir(c.X-r), ir(c.Y-r), ir(c.X+r), ir(c.Y+r)) })
	})
}
