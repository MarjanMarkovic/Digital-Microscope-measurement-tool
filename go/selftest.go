package main

// -selftest <dir>: scripted run of calibration/measurement logic and the camera, for verification.

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"
)

var selfTesting bool

func selfTest(dir string) {
	selfTesting = true
	var out strings.Builder
	logf := func(f string, args ...any) { fmt.Fprintf(&out, f+"\n", args...) }
	defer func() {
		if r := recover(); r != nil {
			logf("PANIC: %v", r)
		}
		os.WriteFile(filepath.Join(dir, "selftest.txt"), []byte(out.String()), 0o644)
		if app.cap != nil {
			app.cap.Stop()
		}
	}()
	calOverride = filepath.Join(dir, "cal_test.json")
	os.Remove(calOverride)
	app.profiles, app.active = map[string]Profile{}, ""
	a, v := app, app.viewer

	// synthetic ruler: 100 px per mm
	W, H := 1280, 960
	pix := make([]byte, W*H*4)
	for i := range pix {
		pix[i] = 200
	}
	for k := 0; k <= 12; k++ {
		for y := 300; y < 400; y++ {
			for x := 100 + k*100 - 1; x <= 100+k*100; x++ {
				i := (y*W + x) * 4
				pix[i], pix[i+1], pix[i+2] = 0, 0, 0
			}
		}
	}
	sendMsg(a.ctl[idFreeze], bmSetCheck, 1, 0)
	v.setImage(W, H, pix)
	v.fitView()
	sc := func(x, y float64) (int, int) { p := v.toScr(pt{x, y}); return int(p.X + 0.5), int(p.Y + 0.5) }
	click := func(x, y float64) { sx, sy := sc(x, y); v.lDown(sx, sy); v.lUp(sx, sy) }

	a.setTool("calibrate")
	click(100, 350)
	sx, sy := sc(1100, 350)
	v.mouseMove(sx, sy, 0)
	click(1100, 350)
	logf("readout: %s", getText(a.ctl[idReadout]))
	// exact endpoints (screen rounding is not what we test here)
	v.cal.Pts = []pt{{100, 350}, {1100, 350}}
	setText(a.ctl[idKnown], "10")
	a.applyCalibration()
	logf("ppm: %.3f  status: %s", v.ppm, strings.ReplaceAll(getText(a.ctl[idCalStatus]), "\r\n", " | "))

	a.setTool("line")
	x0, y0 := sc(300, 350)
	x1, y1 := sc(800, 350)
	v.lDown(x0, y0)
	v.mouseMove(x1, y1, mkLButton)
	v.lUp(x1, y1)
	for _, t := range []struct {
		tool string
		pts  [][2]float64
	}{{"circle", [][2]float64{{600, 600}, {700, 600}}}, {"rect", [][2]float64{{200, 500}, {400, 650}}},
		{"angle", [][2]float64{{900, 500}, {1000, 600}, {1100, 500}}}} {
		a.setTool(t.tool)
		for _, p := range t.pts {
			click(p[0], p[1])
		}
	}
	a.setTool("polyline")
	for _, p := range [][2]float64{{100, 800}, {300, 850}, {500, 800}} {
		click(p[0], p[1])
	}
	v.finishPolyline()
	for _, sh := range v.shapes {
		lab, det := sh.Info(v.ppm)
		logf("%-9s %s  [%s]", sh.Kind, lab, det)
	}
	a.setTool("select")
	px, py := sc(550, 350)
	v.lDown(px, py)
	logf("selected: %d", v.sel)
	v.mouseMove(px+10, py+10, mkLButton)
	v.lUp(px+10, py+10)
	v.deleteSelected()
	logf("shapes after delete: %d, list items: %d", len(v.shapes), sendMsg(a.ctl[idList], 0x18B, 0, 0))
	v.showCross = true
	v.setZoom(3, 300, 300)
	pUpdateWindow.Call(v.hwnd)
	if f, err := os.Create(filepath.Join(dir, "annotated.png")); err == nil {
		png.Encode(f, v.annotatedImage())
		f.Close()
	}
	v.fitView()
	pUpdateWindow.Call(v.hwnd)

	// camera
	cams, err := listCameras()
	logf("cameras: %v err=%v", cams, err)
	if len(cams) > 0 {
		sendMsg(a.ctl[idFreeze], bmSetCheck, 0, 0)
		a.cams = cams
		sendMsg(a.ctl[idCamCombo], cbResetContent, 0, 0)
		for _, c := range cams {
			sendMsg(a.ctl[idCamCombo], cbAddString, 0, uintptr(unsafe.Pointer(u16(c.Name))))
		}
		sendMsg(a.ctl[idCamCombo], cbSetCurSel, 0, 0)
		a.toggleCamera()
		start := time.Now()
		for time.Since(start) < 4*time.Second {
			a.tick()
			time.Sleep(15 * time.Millisecond)
		}
		if a.cap != nil {
			f, act, ready := a.cap.Formats()
			logf("formats ready=%v active=%s", ready, act.label())
			for _, x := range f {
				logf("  %s", x.label())
			}
			var sum int
			for i := 0; i < len(v.bits); i += 4 {
				sum += int(v.bits[i]) + int(v.bits[i+1]) + int(v.bits[i+2])
			}
			logf("mean pixel: %.1f", float64(sum)/float64(len(v.bits)/4*3))
			logf("err=%v frames seq=%d image=%dx%d fps=%.1f", a.cap.Err(), a.lastSeq, v.imgW, v.imgH, a.fps)
		} else {
			logf("capture stopped: %s", a.hint)
		}
		pUpdateWindow.Call(v.hwnd)
		if f, err := os.Create(filepath.Join(dir, "camera.png")); err == nil && v.hasImg() {
			png.Encode(f, v.rawImage())
			f.Close()
		}
	}
	logf("done")
}
