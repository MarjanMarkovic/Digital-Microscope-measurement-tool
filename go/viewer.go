package main

import (
	"fmt"
	"image"
	"math"
	"syscall"
	"unsafe"
)

const (
	handleTol = 9.0
	hitTol    = 7.0
)

type dragState struct {
	handle bool
	sh     *Shape
	idx    int
	ref    pt
	isCal  bool
}

// Viewer is the zoomable image window with the measurement overlay.
type Viewer struct {
	hwnd uintptr
	ui   float64 // DPI scale

	imgW, imgH int
	frameDC    uintptr
	frameBmp   uintptr
	frameOld   uintptr
	bits       []byte // BGRX, top-down, stride imgW*4

	bbDC, bbBmp, bbOld uintptr
	bbW, bbH           int

	scale, ox, oy float64
	fit           bool

	shapes []*Shape
	sel    int
	cal    *Shape
	active *Shape
	tool   string
	ppm    float64

	showScale, showLabels, showCross bool

	drag           *dragState
	panning        bool
	panLast        [2]int
	pressXY        [2]int
	createdOnPress bool

	onShapes func()
	onCal    func()
	onSelect func(int)
	onCursor func(string)
	onSize   func(w, h int)
	onKey    func(vk uintptr) bool
}

var viewerClass = u16("MicroscopeViewerCanvas")
var theViewer *Viewer

func registerViewerClass(inst uintptr) {
	cur, _, _ := pLoadCursorW.Call(0, idcArrow)
	wc := wndClassEx{
		Style:     csDblClks,
		WndProc:   syscall.NewCallback(viewerProc),
		Instance:  inst,
		Cursor:    cur,
		ClassName: viewerClass,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
}

func newViewer(parent, inst uintptr, ui float64) *Viewer {
	v := &Viewer{ui: ui, scale: 1, fit: true, sel: -1, tool: "select", showScale: true, showLabels: true}
	v.onShapes, v.onCal = func() {}, func() {}
	v.onSelect, v.onCursor, v.onSize = func(int) {}, func(string) {}, func(int, int) {}
	theViewer = v
	h, _, _ := pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(viewerClass)), 0, wsChild|wsVisible,
		0, 0, 100, 100, parent, 0, inst, 0)
	v.hwnd = h
	v.frameDC, _, _ = pCreateCompatibleDC.Call(0)
	return v
}

func viewerProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	v := theViewer
	if v == nil || v.hwnd == 0 {
		r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
		return r
	}
	x, y := loWord(lp), hiWord(lp)
	switch msg {
	case wmPaint:
		v.paint()
		return 0
	case wmEraseBkgnd:
		return 1
	case wmSize:
		if v.fit {
			v.fitView()
		}
		invalidate(hwnd)
		return 0
	case wmSetCursor:
		if loWord(lp) == htClient {
			id := uintptr(idcCross)
			if v.tool == "select" {
				id = idcArrow
			}
			c, _, _ := pLoadCursorW.Call(0, id)
			pSetCursor.Call(c)
			return 1
		}
	case wmLButtonDown:
		v.lDown(x, y)
		return 0
	case wmLButtonDblClk:
		if v.active != nil && v.active.Kind == "polyline" {
			v.finishPolyline()
		} else {
			v.lDown(x, y)
		}
		return 0
	case wmLButtonUp:
		v.lUp(x, y)
		pReleaseCapture.Call()
		return 0
	case wmMouseMove:
		v.mouseMove(x, y, wp)
		return 0
	case wmRButtonDown:
		v.rDown()
		return 0
	case wmMButtonDown:
		pSetFocus.Call(hwnd)
		pSetCapture.Call(hwnd)
		v.panning, v.panLast = true, [2]int{x, y}
		return 0
	case wmMButtonUp:
		v.panning = false
		pReleaseCapture.Call()
		return 0
	case wmCaptureChanged:
		if v.drag != nil && !v.drag.isCal {
			v.onShapes()
		}
		v.drag, v.panning = nil, false
		return 0
	case wmMouseWheel:
		p := point{int32(x), int32(y)}
		pScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&p)))
		delta := float64(int16(wp >> 16))
		v.setZoom(v.scale*math.Pow(1.25, delta/120), float64(p.X), float64(p.Y))
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
	return r
}

func (v *Viewer) hasImg() bool { return v.bits != nil }

func (v *Viewer) toImg(x, y float64) pt { return pt{(x - v.ox) / v.scale, (y - v.oy) / v.scale} }
func (v *Viewer) toScr(p pt) pt         { return pt{p.X*v.scale + v.ox, p.Y*v.scale + v.oy} }

func (v *Viewer) redraw() { invalidate(v.hwnd) }

// ensureImage (re)allocates the frame buffer; a size change clears all measurements.
func (v *Viewer) ensureImage(w, h int) {
	if v.bits != nil && w == v.imgW && h == v.imgH {
		pGdiFlush.Call()
		return
	}
	if v.frameBmp != 0 {
		pSelectObject.Call(v.frameDC, v.frameOld)
		pDeleteObject.Call(v.frameBmp)
	}
	v.frameBmp, v.bits = createDIB(w, h)
	if v.frameBmp == 0 {
		v.bits, v.imgW, v.imgH = nil, 0, 0
		return
	}
	v.frameOld, _, _ = pSelectObject.Call(v.frameDC, v.frameBmp)
	v.imgW, v.imgH = w, h
	v.shapes, v.sel, v.active, v.cal, v.drag = nil, -1, nil, nil, nil
	v.fitView()
	v.onSize(w, h)
	v.onSelect(-1)
	v.onShapes()
	v.onCal()
}

// setImage copies a BGRX frame (stride w*4) into the view.
func (v *Viewer) setImage(w, h int, src []byte) {
	v.ensureImage(w, h)
	if v.bits != nil {
		copy(v.bits, src)
	}
	v.redraw()
}

func (v *Viewer) fitView() {
	v.fit = true
	if !v.hasImg() {
		return
	}
	W, H := clientSize(v.hwnd)
	W, H = max(W, 2), max(H, 2)
	v.scale = math.Min(float64(W)/float64(v.imgW), float64(H)/float64(v.imgH))
	v.ox = (float64(W) - float64(v.imgW)*v.scale) / 2
	v.oy = (float64(H) - float64(v.imgH)*v.scale) / 2
	v.redraw()
}

func (v *Viewer) setZoom(s, ax, ay float64) {
	if !v.hasImg() {
		return
	}
	if math.IsNaN(ax) {
		W, H := clientSize(v.hwnd)
		ax, ay = float64(W)/2, float64(H)/2
	}
	s = math.Max(0.02, math.Min(64, s))
	v.ox = ax - (ax-v.ox)*s/v.scale
	v.oy = ay - (ay-v.oy)*s/v.scale
	v.scale, v.fit = s, false
	v.redraw()
}

// ---- tools

func (v *Viewer) setTool(t string) {
	v.tool, v.active = t, nil
	v.redraw()
}

func (v *Viewer) selectIdx(i int) {
	if i != v.sel {
		v.sel = i
		v.onSelect(i)
	}
	v.redraw()
}

func (v *Viewer) deleteSelected() {
	if v.sel >= 0 && v.sel < len(v.shapes) {
		v.shapes = append(v.shapes[:v.sel], v.shapes[v.sel+1:]...)
		v.sel = -1
		v.onSelect(-1)
		v.onShapes()
		v.redraw()
	}
}

func (v *Viewer) clearAll() {
	v.shapes, v.sel, v.active = nil, -1, nil
	v.onSelect(-1)
	v.onShapes()
	v.redraw()
}

func (v *Viewer) clearCal() {
	v.cal = nil
	v.onCal()
	v.redraw()
}

func (v *Viewer) cancel() {
	v.active = nil
	v.redraw()
}

func (v *Viewer) finishPolyline() {
	if a := v.active; a != nil && a.Kind == "polyline" {
		a.Pts = a.Pts[:len(a.Pts)-1]
		v.commit(a)
	}
}

// ---- hit testing (screen space)

func (v *Viewer) hitHandle(sh *Shape, x, y int) int {
	q := pt{float64(x), float64(y)}
	for i, p := range sh.Pts {
		if dist(v.toScr(p), q) <= handleTol*v.ui {
			return i
		}
	}
	return -1
}

func (v *Viewer) hitShape(sh *Shape, x, y int) bool {
	pts := make([]pt, len(sh.Pts))
	for i, p := range sh.Pts {
		pts[i] = v.toScr(p)
	}
	t, q := hitTol*v.ui, pt{float64(x), float64(y)}
	switch sh.Kind {
	case "line", "polyline":
		for i := 0; i+1 < len(pts); i++ {
			if segDist(q, pts[i], pts[i+1]) <= t {
				return true
			}
		}
	case "angle":
		return segDist(q, pts[0], pts[1]) <= t || segDist(q, pts[1], pts[2]) <= t
	case "circle":
		return math.Abs(dist(q, pts[0])-dist(pts[0], pts[1])) <= t || dist(q, pts[0]) <= t
	case "rect":
		a, b := pts[0], pts[1]
		c := []pt{a, {b.X, a.Y}, b, {a.X, b.Y}}
		for i := 0; i < 4; i++ {
			if segDist(q, c[i], c[(i+1)%4]) <= t {
				return true
			}
		}
	}
	return false
}

// ---- mouse

func (v *Viewer) lDown(x, y int) {
	pSetFocus.Call(v.hwnd)
	pSetCapture.Call(v.hwnd)
	if !v.hasImg() {
		return
	}
	p := v.toImg(float64(x), float64(y))
	v.pressXY = [2]int{x, y}
	v.createdOnPress = false
	if v.active != nil {
		v.addPoint(p)
		return
	}
	if v.tool == "select" {
		if v.sel >= 0 && v.sel < len(v.shapes) {
			if idx := v.hitHandle(v.shapes[v.sel], x, y); idx >= 0 {
				v.drag = &dragState{handle: true, sh: v.shapes[v.sel], idx: idx}
				return
			}
		}
		for i := len(v.shapes) - 1; i >= 0; i-- {
			if v.hitShape(v.shapes[i], x, y) {
				v.selectIdx(i)
				v.drag = &dragState{sh: v.shapes[i], ref: p}
				return
			}
		}
		v.selectIdx(-1)
		v.panning, v.panLast = true, [2]int{x, y}
		return
	}
	if v.tool == "calibrate" && v.cal != nil {
		if idx := v.hitHandle(v.cal, x, y); idx >= 0 {
			v.drag = &dragState{handle: true, sh: v.cal, idx: idx, isCal: true}
			return
		}
	}
	v.active = &Shape{Kind: v.tool, Pts: []pt{p, p}}
	v.createdOnPress = true
	v.redraw()
}

func (v *Viewer) addPoint(p pt) {
	a := v.active
	a.Pts[len(a.Pts)-1] = p
	if n := need[a.Kind]; n > 0 && len(a.Pts) >= n {
		v.commit(a)
	} else {
		a.Pts = append(a.Pts, p)
	}
	v.redraw()
}

func (v *Viewer) commit(sh *Shape) {
	v.active = nil
	if !sh.degenerate() {
		if sh.Kind == "calibrate" {
			v.cal = sh
			v.onCal()
		} else {
			v.shapes = append(v.shapes, sh)
			v.onShapes()
		}
	}
	v.redraw()
}

func (v *Viewer) rDown() {
	a := v.active
	if a == nil {
		return
	}
	if a.Kind == "polyline" && len(a.Pts) > 2 {
		v.finishPolyline()
	} else {
		v.cancel()
	}
}

func (v *Viewer) mouseMove(x, y int, wp uintptr) {
	if !v.hasImg() {
		return
	}
	p := v.toImg(float64(x), float64(y))
	if v.drag != nil && wp&mkLButton != 0 {
		d := v.drag
		if d.handle {
			d.sh.Pts[d.idx] = p
		} else {
			dx, dy := p.X-d.ref.X, p.Y-d.ref.Y
			for i := range d.sh.Pts {
				d.sh.Pts[i].X += dx
				d.sh.Pts[i].Y += dy
			}
			d.ref = p
		}
		if d.isCal {
			v.onCal()
		}
		v.redraw()
		return
	}
	if v.panning {
		v.ox += float64(x - v.panLast[0])
		v.oy += float64(y - v.panLast[1])
		v.panLast = [2]int{x, y}
		v.fit = false
		v.redraw()
		return
	}
	if v.active != nil {
		v.active.Pts[len(v.active.Pts)-1] = p
		v.redraw()
	}
	txt := "outside image"
	if p.X >= 0 && p.Y >= 0 && p.X < float64(v.imgW) && p.Y < float64(v.imgH) {
		txt = fmt.Sprintf("x %.1f  y %.1f px", p.X, p.Y)
	}
	v.onCursor(fmt.Sprintf("%s    zoom %.0f%%", txt, v.scale*100))
}

func (v *Viewer) lUp(x, y int) {
	if v.drag != nil {
		if !v.drag.isCal {
			v.onShapes()
		}
		v.drag = nil
	}
	v.panning = false
	if a := v.active; a != nil && v.createdOnPress && need[a.Kind] == 2 {
		if math.Hypot(float64(x-v.pressXY[0]), float64(y-v.pressXY[1])) > 6 {
			v.addPoint(v.toImg(float64(x), float64(y)))
		}
	}
	v.createdOnPress = false
}

// ---- painting

func (v *Viewer) paint() {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(v.hwnd, uintptr(unsafe.Pointer(&ps)))
	defer pEndPaint.Call(v.hwnd, uintptr(unsafe.Pointer(&ps)))
	W, H := clientSize(v.hwnd)
	if W < 2 || H < 2 {
		return
	}
	if v.bbDC == 0 || v.bbW != W || v.bbH != H {
		if v.bbDC != 0 {
			pSelectObject.Call(v.bbDC, v.bbOld)
			pDeleteObject.Call(v.bbBmp)
			pDeleteDC.Call(v.bbDC)
		}
		v.bbDC, _, _ = pCreateCompatibleDC.Call(hdc)
		v.bbBmp, _, _ = pCreateCompatibleBitmap.Call(hdc, uintptr(W), uintptr(H))
		v.bbOld, _, _ = pSelectObject.Call(v.bbDC, v.bbBmp)
		v.bbW, v.bbH = W, H
	}
	dc := v.bbDC
	bg, _, _ := pCreateSolidBrush.Call(uintptr(rgb(0x20, 0x20, 0x20)))
	rc := rect{0, 0, int32(W), int32(H)}
	pFillRect.Call(dc, uintptr(unsafe.Pointer(&rc)), bg)
	pDeleteObject.Call(bg)

	g := newGfx(dc, v.ui)
	defer g.close()
	if !v.hasImg() {
		msg := "No image - start a camera or open an image file"
		tw, th := g.textSize(msg, int(14*v.ui))
		g.text(float64(W-tw)/2, float64(H-th)/2, msg, rgb(0x88, 0x88, 0x88), 14*v.ui, false, false)
		pBitBlt.Call(hdc, 0, 0, uintptr(W), uintptr(H), dc, 0, 0, srcCopy)
		return
	}

	// visible source rectangle (integer pixels) and its exact destination
	s := v.scale
	x0 := max(0, int(math.Floor(-v.ox/s)))
	y0 := max(0, int(math.Floor(-v.oy/s)))
	x1 := min(v.imgW, int(math.Ceil((float64(W)-v.ox)/s)))
	y1 := min(v.imgH, int(math.Ceil((float64(H)-v.oy)/s)))
	if x1 > x0 && y1 > y0 {
		dx := int(math.Round(v.ox + float64(x0)*s))
		dy := int(math.Round(v.oy + float64(y0)*s))
		dw := int(math.Round(v.ox+float64(x1)*s)) - dx
		dh := int(math.Round(v.oy+float64(y1)*s)) - dy
		if s < 1 {
			pSetStretchBltMode.Call(dc, halftone)
			pSetBrushOrgEx.Call(dc, 0, 0, 0)
		} else {
			pSetStretchBltMode.Call(dc, colorOnColor)
		}
		pStretchBlt.Call(dc, uintptr(dx), uintptr(dy), uintptr(dw), uintptr(dh),
			v.frameDC, uintptr(x0), uintptr(y0), uintptr(x1-x0), uintptr(y1-y0), srcCopy)
	}

	for i, sh := range v.shapes {
		col := colShape
		if i == v.sel {
			col = colSel
		}
		drawShape(g, sh, v.toScr, v.ppm, col, v.showLabels, i == v.sel && v.tool == "select")
	}
	if v.cal != nil {
		drawShape(g, v.cal, v.toScr, 0, colCal, true, v.tool == "calibrate")
	}
	if v.active != nil {
		col := colActive
		if v.active.Kind == "calibrate" {
			col = colCal
		}
		drawShape(g, v.active, v.toScr, v.ppm, col, true, false)
	}
	if v.showCross {
		drawCrosshair(g, v.toScr(pt{float64(v.imgW) / 2, float64(v.imgH) / 2}), W, H)
	}
	if v.showScale && v.ppm > 0 {
		drawScalebar(g, W, H, v.ppm*v.scale)
	}
	pBitBlt.Call(hdc, 0, 0, uintptr(W), uintptr(H), dc, 0, 0, srcCopy)
}

// rawImage returns the current frame as RGBA.
func (v *Viewer) rawImage() *image.RGBA {
	pGdiFlush.Call()
	return bgrxToRGBA(v.bits, v.imgW, v.imgH)
}

// annotatedImage renders the measurements onto a full-resolution copy of the frame.
func (v *Viewer) annotatedImage() *image.RGBA {
	W, H := v.imgW, v.imgH
	dc, _, _ := pCreateCompatibleDC.Call(0)
	bmp, bits := createDIB(W, H)
	if bmp == 0 {
		pDeleteDC.Call(dc)
		return v.rawImage()
	}
	old, _, _ := pSelectObject.Call(dc, bmp)
	pGdiFlush.Call()
	copy(bits, v.bits)
	g := newGfx(dc, math.Max(1, float64(max(W, H))/1000))
	id := func(p pt) pt { return p }
	for _, sh := range v.shapes {
		drawShape(g, sh, id, v.ppm, colShape, true, false)
	}
	if v.showScale && v.ppm > 0 {
		drawScalebar(g, W, H, v.ppm)
	}
	g.close()
	pGdiFlush.Call()
	img := bgrxToRGBA(bits, W, H)
	pSelectObject.Call(dc, old)
	pDeleteObject.Call(bmp)
	pDeleteDC.Call(dc)
	return img
}

func bgrxToRGBA(src []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	d := img.Pix
	for i := 0; i+3 < len(src) && i+3 < len(d); i += 4 {
		d[i], d[i+1], d[i+2], d[i+3] = src[i+2], src[i+1], src[i], 255
	}
	return img
}
