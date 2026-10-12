package main

import (
	"encoding/csv"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

const (
	idCamCombo = 100 + iota
	idRefresh
	idResCombo
	idStart
	idFreeze
	idOpen
	idSaveRaw
	idSaveAnn
	idToolSelect
	idToolLine
	idToolPoly
	idToolAngle
	idToolCircle
	idToolRect
	idScaleBar
	idLabels
	idCross
	idZoom11
	idFit
	idProfCombo
	idProfDelete
	idCalStatus
	idToolCal
	idCalClear
	idKnown
	idName
	idReadout
	idApply
	idList
	idDelete
	idClearAll
	idExport
	idStatic
)

var toolByID = map[int]string{idToolSelect: "select", idToolLine: "line", idToolPoly: "polyline",
	idToolAngle: "angle", idToolCircle: "circle", idToolRect: "rect", idToolCal: "calibrate"}

var toolHint = map[string]string{
	"select":    "Select/Move: click a measurement to select, drag it or its handles; Delete removes it. Drag empty space to pan.",
	"line":      "Distance: click two points (or drag).",
	"polyline":  "Path: click points, double-click or right-click to finish.",
	"angle":     "Angle: click first arm end, the vertex, then second arm end.",
	"circle":    "Circle: click the centre, then a point on the edge.",
	"rect":      "Rectangle: click two opposite corners.",
	"calibrate": "Calibration: click two ruler marks a known distance apart (zoom in for precision; drag the ends to adjust).",
}

var hotkeys = map[uintptr]string{'V': "select", 'L': "line", 'P': "polyline", 'A': "angle", 'C': "circle", 'R': "rect", 'K': "calibrate"}

// layout anchors
const (
	anchorTop = iota
	anchorStretch
	anchorBottom
)

type item struct {
	h           uintptr // 0 for a painted group frame
	title       string
	rc          rect // last laid-out position
	x, y, w, ht int
	anchor      int
	pad         int // stretch: space left below; bottom: distance from bottom
}

type resMode struct{ w, h int } // 0,0 max; -1,-1 default

type App struct {
	hwnd, inst uintptr
	ui         float64
	font, bold uintptr
	items      []item
	ctl        map[int]uintptr
	status     uintptr
	viewer     *Viewer
	cams       []camDev
	cap        *Capture
	lastSeq    uint64
	fmtShown   bool
	resModes   []resMode
	req        resMode
	profiles   map[string]Profile
	active     string
	calOK      bool
	frames     int
	fpsT       time.Time
	fps        float64
	hint       string
	panelW     int
}

var app *App

func init() { runtime.LockOSThread() }

func main() {
	pCoInitializeEx.Call(0, 2) // STA for the UI thread (file dialogs)
	if err := mfStartup(); err != nil {
		msgBox(0, "Media Foundation is not available: "+err.Error()+"\nCamera capture will not work.", "Microscope Viewer", mbIconError)
	}
	icc := initCommonControlsEx{Size: 8, ICC: 0x4 | 0xFF}
	pInitCommonControlsEx.Call(uintptr(unsafe.Pointer(&icc)))

	inst, _, _ := pGetModuleHandleW.Call(0)
	dpi, _, _ := pGetDpiForSystem.Call()
	if dpi == 0 {
		dpi = 96
	}
	app = &App{inst: inst, ui: float64(dpi) / 96, ctl: map[int]uintptr{}, fpsT: time.Now()}
	app.profiles, app.active = loadCalibrations()
	app.createWindow()
	if len(os.Args) > 2 && os.Args[1] == "-selftest" {
		selfTest(os.Args[2])
		return
	}
	app.messageLoop()
}

func (a *App) s(v int) int { return int(math.Round(float64(v) * a.ui)) }

func (a *App) createWindow() {
	cls := u16("MicroscopeViewerMain")
	cur, _, _ := pLoadCursorW.Call(0, idcArrow)
	bg, _, _ := pGetSysColorBrush.Call(colorBtnFace)
	icon, _, _ := pLoadImageW.Call(a.inst, uintptr(unsafe.Pointer(u16("APP"))), 1, 0, 0, 0x40)
	iconSm, _, _ := pLoadImageW.Call(a.inst, uintptr(unsafe.Pointer(u16("APP"))), 1, uintptr(a.s(16)), uintptr(a.s(16)), 0)
	wc := wndClassEx{
		Style: csHRedraw | csVRedraw, WndProc: syscall.NewCallback(mainProc), Instance: a.inst,
		Cursor: cur, Background: bg, ClassName: cls, Icon: icon, IconSm: iconSm,
	}
	wc.Size = uint32(unsafe.Sizeof(wc))
	pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	registerViewerClass(a.inst)

	const cwUseDefault = 0x80000000
	a.hwnd, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(u16("Microscope Viewer"))),
		wsOverlappedWindow|wsClipChildren, cwUseDefault, cwUseDefault, uintptr(a.s(1400)), uintptr(a.s(900)), 0, 0, a.inst, 0)
	a.build()
	a.layout()
	if len(os.Args) > 1 && os.Args[1] == "-selftest" {
		pShowWindow.Call(a.hwnd, 1)
	} else {
		pShowWindow.Call(a.hwnd, swMaximize)
	}
	pUpdateWindow.Call(a.hwnd)
	pSetTimer.Call(a.hwnd, 1, 15, 0)
	pPostMessageW.Call(a.hwnd, wmUser+1, 0, 0) // deferred startup
}

func (a *App) messageLoop() {
	var m msgT
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		if m.Message == wmKeyDown && a.handleKey(m.WParam) {
			continue
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (a *App) handleKey(vk uintptr) bool {
	f, _, _ := pGetFocus.Call()
	if f != 0 && strings.EqualFold(className(f), "Edit") {
		return false
	}
	if ks, _, _ := pGetKeyState.Call(vkControl); int16(ks) < 0 {
		return false
	}
	v := a.viewer
	switch vk {
	case vkEscape:
		v.cancel()
		return true
	case vkReturn:
		v.finishPolyline()
		return true
	case vkDelete, vkBack:
		v.deleteSelected()
		return true
	}
	if t, ok := hotkeys[vk]; ok {
		a.setTool(t)
		return true
	}
	return false
}

// ---- controls

func (a *App) add(class, text string, style uint32, exStyle uint32, id, x, y, w, h int) uintptr {
	hw, _, _ := pCreateWindowExW.Call(uintptr(exStyle), uintptr(unsafe.Pointer(u16(class))), uintptr(unsafe.Pointer(u16(text))),
		uintptr(wsChild|wsVisible|style), 0, 0, 10, 10, a.hwnd, uintptr(id), a.inst, 0)
	sendMsg(hw, wmSetFont, a.font, 1)
	a.items = append(a.items, item{h: hw, x: x, y: y, w: w, ht: h})
	if id != idStatic {
		a.ctl[id] = hw
	}
	return hw
}

// group adds a titled frame; it is painted by the main window (see paintGroups),
// not a BS_GROUPBOX child, so the background inside it is always repainted.
func (a *App) group(text string, x, y, w, h int) {
	a.items = append(a.items, item{title: text, x: x, y: y, w: w, ht: h})
}
func (a *App) label(text string, id, x, y, w, h int) uintptr {
	return a.add("STATIC", text, ssNoPrefix, 0, id, x, y, w, h)
}
func (a *App) button(text string, id, x, y, w, h int) uintptr {
	return a.add("BUTTON", text, bsPushButton|wsTabStop, 0, id, x, y, w, h)
}
func (a *App) check(text string, id, x, y, w, h int, on bool) uintptr {
	hw := a.add("BUTTON", text, bsAutoCheckbox|wsTabStop, 0, id, x, y, w, h)
	if on {
		sendMsg(hw, bmSetCheck, 1, 0)
	}
	return hw
}
func (a *App) radio(text string, id, x, y, w, h int) uintptr {
	return a.add("BUTTON", text, bsRadioButton|wsTabStop, 0, id, x, y, w, h)
}
func (a *App) combo(id, x, y, w int) uintptr {
	return a.add("COMBOBOX", "", cbsDropDownList|cbsHasStrings|wsVScroll|wsTabStop, 0, id, x, y, w, 300)
}
func (a *App) edit(text string, id, x, y, w, h int) uintptr {
	return a.add("EDIT", text, esAutoHScroll|wsTabStop, wsExClientEdge, id, x, y, w, h)
}

func (a *App) build() {
	mk := func(pt int, weight int) uintptr {
		f, _, _ := pCreateFontW.Call(uintptr(uint32(-int32(math.Round(float64(pt)*96*a.ui/72)))), 0, 0, 0, uintptr(weight),
			0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(u16("Segoe UI"))))
		return f
	}
	a.font, a.bold = mk(9, 400), mk(9, 700)
	a.panelW = 360

	a.viewer = newViewer(a.hwnd, a.inst, a.ui)
	v := a.viewer
	v.onShapes = a.refreshList
	v.onCal = a.updateReadout
	v.onSelect = a.listSelect
	v.onCursor = func(s string) { a.setStatus(1, s) }
	v.onSize = func(w, h int) { a.updatePPM() }

	a.group("Camera", 0, 0, 360, 142)
	a.combo(idCamCombo, 10, 20, 256)
	a.button("Refresh", idRefresh, 272, 19, 78, 25)
	a.label("Resolution", idStatic, 10, 53, 72, 20)
	a.combo(idResCombo, 84, 49, 266)
	a.button("Start", idStart, 10, 80, 252, 27)
	a.check("Freeze", idFreeze, 272, 83, 78, 22, false)
	a.button("Open image...", idOpen, 10, 111, 108, 26)
	a.button("Save raw", idSaveRaw, 122, 111, 100, 26)
	a.button("Save + overlay", idSaveAnn, 226, 111, 124, 26)

	a.group("Measure", 0, 146, 360, 126)
	tools := []struct {
		id   int
		text string
	}{{idToolSelect, "Select/Move (V)"}, {idToolLine, "Distance (L)"}, {idToolPoly, "Path (P)"},
		{idToolAngle, "Angle (A)"}, {idToolCircle, "Circle (C)"}, {idToolRect, "Rectangle (R)"}}
	for i, t := range tools {
		a.radio(t.text, t.id, 12+(i%2)*172, 166+(i/2)*24, 166, 22)
	}
	a.check("Scale bar", idScaleBar, 12, 241, 84, 22, true)
	a.check("Labels", idLabels, 98, 241, 66, 22, true)
	a.check("Crosshair", idCross, 166, 241, 88, 22, false)
	a.button("1:1", idZoom11, 260, 240, 42, 25)
	a.button("Fit", idFit, 308, 240, 42, 25)

	a.group("Calibration", 0, 276, 360, 250)
	a.label("Active", idStatic, 10, 300, 44, 20)
	a.combo(idProfCombo, 56, 296, 210)
	a.button("Delete", idProfDelete, 272, 295, 78, 25)
	a.label("", idCalStatus, 10, 326, 340, 34)
	a.radio("1. Draw calibration line (K)", idToolCal, 12, 364, 250, 22)
	a.button("Clear", idCalClear, 272, 362, 78, 25)
	a.label("2. Known distance", idStatic, 10, 397, 118, 20)
	a.edit("10.000", idKnown, 130, 393, 80, 24)
	a.label("mm", idStatic, 216, 397, 30, 20)
	a.label("3. Name", idStatic, 10, 427, 60, 20)
	a.edit("10x objective", idName, 74, 423, 276, 24)
	sendMsg(a.label("Measured: - (draw a calibration line)", idReadout, 10, 455, 340, 20), wmSetFont, a.bold, 1)
	a.button("4. Apply && save calibration", idApply, 10, 480, 340, 30)

	a.group("Measurements", 0, 530, 360, 0)
	a.items[len(a.items)-1].anchor, a.items[len(a.items)-1].pad = anchorStretch, 0
	lb := a.add("LISTBOX", "", lbsNotify|lbsUseTabStops|lbsNoIntegralHeight|wsVScroll|wsTabStop, wsExClientEdge, idList, 10, 550, 340, 0)
	tabs := []int32{74}
	sendMsg(lb, lbSetTabStops, 1, uintptr(unsafe.Pointer(&tabs[0])))
	a.items[len(a.items)-1].anchor, a.items[len(a.items)-1].pad = anchorStretch, 42
	for _, b := range []struct {
		id, x, w int
		t        string
	}{{idDelete, 10, 80, "Delete"}, {idClearAll, 96, 80, "Clear all"}, {idExport, 240, 110, "Export CSV..."}} {
		a.button(b.t, b.id, b.x, 0, b.w, 26)
		a.items[len(a.items)-1].anchor, a.items[len(a.items)-1].pad = anchorBottom, 36
	}

	a.status, _, _ = pCreateWindowExW.Call(0, uintptr(unsafe.Pointer(u16("msctls_statusbar32"))), 0,
		wsChild|wsVisible|0x100, 0, 0, 0, 0, a.hwnd, 0, a.inst, 0)
	sendMsg(a.status, wmSetFont, a.font, 1)

	a.setTool("select")
	a.refreshProfiles()
	a.updatePPM()
	a.updateReadout()
	a.fillResCombo(nil)
}

func (a *App) layout() {
	W, H := clientSize(a.hwnd)
	if W == 0 || H == 0 {
		return
	}
	sendMsg(a.status, wmSize, 0, 0)
	var sr rect
	pGetWindowRect.Call(a.status, uintptr(unsafe.Pointer(&sr)))
	sh := int(sr.Bottom - sr.Top)
	parts := []int32{int32(max(W-a.s(420), 100)), -1}
	sendMsg(a.status, sbSetParts, 2, uintptr(unsafe.Pointer(&parts[0])))

	m := a.s(6)
	pw := a.s(a.panelW)
	px := W - pw - m
	bottom := H - sh - m
	pMoveWindow.Call(a.viewer.hwnd, uintptr(m), uintptr(m), uintptr(max(px-2*m, 10)), uintptr(max(bottom-m, 10)), 1)
	for i, it := range a.items {
		x, y, w, h := px+a.s(it.x), m+a.s(it.y), a.s(it.w), a.s(it.ht)
		switch it.anchor {
		case anchorStretch:
			h = max(bottom-y-a.s(it.pad), a.s(30))
		case anchorBottom:
			y = bottom - a.s(it.pad)
		}
		a.items[i].rc = rect{int32(x), int32(y), int32(x + w), int32(y + h)}
		if it.h != 0 {
			pMoveWindow.Call(it.h, uintptr(x), uintptr(y), uintptr(w), uintptr(h), 1)
		}
	}
	pInvalidateRect.Call(a.hwnd, 0, 1)
}

func (a *App) paintGroups() {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(a.hwnd, uintptr(unsafe.Pointer(&ps)))
	defer pEndPaint.Call(a.hwnd, uintptr(unsafe.Pointer(&ps)))
	old, _, _ := pSelectObject.Call(hdc, a.font)
	defer pSelectObject.Call(hdc, old)
	face, _, _ := pGetSysColor.Call(colorBtnFace)
	text, _, _ := pGetSysColor.Call(colorBtnText)
	pSetBkMode.Call(hdc, bkOpaque)
	pSetBkColor.Call(hdc, face)
	pSetTextColor.Call(hdc, text)
	for _, it := range a.items {
		if it.h != 0 || it.title == "" {
			continue
		}
		u := windows.StringToUTF16(it.title)
		var sz size
		pGetTextExtentPoint32W.Call(hdc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&sz)))
		frame := it.rc
		frame.Top += sz.CY / 2
		pDrawEdge.Call(hdc, uintptr(unsafe.Pointer(&frame)), edgeEtched, bfRect)
		pad := int32(a.s(4))
		pExtTextOutW.Call(hdc, uintptr(it.rc.Left+2*pad), uintptr(it.rc.Top), 0, 0,
			uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), 0)
	}
}

func (a *App) setStatus(part int, s string) {
	if part == 0 {
		a.hint = s
	}
	sendMsg(a.status, sbSetTextW, uintptr(part), uintptr(unsafe.Pointer(u16(s))))
}

func (a *App) checked(id int) bool { return sendMsg(a.ctl[id], bmGetCheck, 0, 0) == 1 }

func (a *App) setTool(t string) {
	for id, name := range toolByID {
		on := uintptr(0)
		if name == t {
			on = 1
		}
		sendMsg(a.ctl[id], bmSetCheck, on, 0)
	}
	a.viewer.setTool(t)
	a.setStatus(0, toolHint[t])
}

// ---- window procedure

func mainProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	a := app
	if a == nil || a.hwnd == 0 {
		r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
		return r
	}
	switch msg {
	case wmSize:
		a.layout()
		return 0
	case wmPaint:
		a.paintGroups()
		return 0
	case wmGetMinMaxInfo:
		mi := (*minMaxInfo)(unsafe.Pointer(lp))
		mi.MinTrackSize = point{int32(a.s(900)), int32(a.s(640))}
		return 0
	case wmTimer:
		a.tick()
		return 0
	case wmUser + 1:
		a.refreshCameras()
		if len(a.cams) > 0 {
			a.toggleCamera()
		}
		return 0
	case wmCtlColorStatic:
		hdc := wp
		pSetBkMode.Call(hdc, bkTransparent)
		if lp == a.ctl[idCalStatus] {
			col := rgb(0xc6, 0x28, 0x28)
			if a.calOK {
				col = rgb(0x1b, 0x5e, 0x20)
			}
			pSetTextColor.Call(hdc, uintptr(col))
		}
		br, _, _ := pGetSysColorBrush.Call(colorBtnFace)
		return br
	case wmCommand:
		a.command(int(wp&0xffff), int((wp>>16)&0xffff))
		return 0
	case wmClose:
		if a.cap != nil {
			a.cap.Stop()
			a.cap = nil
		}
		pDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(hwnd, uintptr(msg), wp, lp)
	return r
}

func (a *App) command(id, code int) {
	v := a.viewer
	if t, ok := toolByID[id]; ok && code == bnClicked {
		a.setTool(t)
		return
	}
	switch id {
	case idRefresh:
		a.refreshCameras()
	case idCamCombo, idResCombo:
		if code == cbnSelChange {
			if id == idCamCombo {
				a.fillResCombo(nil)
				a.req = resMode{}
			} else {
				i := int(sendMsg(a.ctl[idResCombo], cbGetCurSel, 0, 0))
				if i >= 0 && i < len(a.resModes) {
					a.req = a.resModes[i]
				}
			}
			if a.cap != nil {
				a.stopCamera()
				a.toggleCamera()
			}
		}
	case idStart:
		a.toggleCamera()
	case idOpen:
		a.openImage()
	case idSaveRaw:
		a.saveImage(false)
	case idSaveAnn:
		a.saveImage(true)
	case idScaleBar, idLabels, idCross:
		v.showScale, v.showLabels, v.showCross = a.checked(idScaleBar), a.checked(idLabels), a.checked(idCross)
		v.redraw()
	case idZoom11:
		v.setZoom(1, math.NaN(), math.NaN())
	case idFit:
		v.fitView()
	case idProfCombo:
		if code == cbnSelChange {
			i := int(sendMsg(a.ctl[idProfCombo], cbGetCurSel, 0, 0))
			names := profileNames(a.profiles)
			a.active = ""
			if i >= 1 && i-1 < len(names) {
				a.active = names[i-1]
			}
			a.saveCal()
			a.updatePPM()
		}
	case idProfDelete:
		if a.active != "" && msgBox(a.hwnd, fmt.Sprintf("Delete calibration '%s'?", a.active), "Calibration", mbYesNo|mbIconQuestion) == idYes {
			delete(a.profiles, a.active)
			a.active = ""
			a.saveCal()
			a.refreshProfiles()
			a.updatePPM()
		}
	case idCalClear:
		v.clearCal()
	case idKnown:
		if code == enChange {
			a.updateReadout()
		}
	case idApply:
		a.applyCalibration()
	case idList:
		if code == lbnSelChange {
			i := int(sendMsg(a.ctl[idList], lbGetCurSel, 0, 0))
			if i >= 0 {
				v.selectIdx(i)
			}
		}
	case idDelete:
		v.deleteSelected()
	case idClearAll:
		v.clearAll()
	case idExport:
		a.exportCSV()
	}
}

// ---- camera

func (a *App) refreshCameras() {
	cur, _, _ := pLoadCursorW.Call(0, idcWait)
	pSetCursor.Call(cur)
	cams, err := listCameras()
	a.cams = cams
	cb := a.ctl[idCamCombo]
	sendMsg(cb, cbResetContent, 0, 0)
	sel := 0
	for i, c := range cams {
		sendMsg(cb, cbAddString, 0, uintptr(unsafe.Pointer(u16(c.Name))))
		if a.cap != nil && c.Link == a.cap.Link {
			sel = i
		}
	}
	if len(cams) > 0 {
		sendMsg(cb, cbSetCurSel, uintptr(sel), 0)
	}
	switch {
	case err != nil:
		a.setStatus(0, "Camera enumeration failed: "+err.Error())
	case len(cams) == 0:
		a.setStatus(0, "No cameras found - plug in the UVC camera and press Refresh")
	default:
		a.setStatus(0, fmt.Sprintf("%d camera(s) found", len(cams)))
	}
}

func (a *App) toggleCamera() {
	if a.cap != nil {
		a.stopCamera()
		a.setStatus(0, "Camera stopped")
		return
	}
	i := int(sendMsg(a.ctl[idCamCombo], cbGetCurSel, 0, 0))
	if i < 0 || i >= len(a.cams) {
		msgBox(a.hwnd, "No camera selected. Plug in the UVC camera and press Refresh.", "Camera", mbIconInfo)
		return
	}
	c := a.cams[i]
	a.cap = startCapture(c.Link, c.Name, a.req.w, a.req.h)
	a.lastSeq, a.fmtShown, a.frames, a.fpsT = 0, false, 0, time.Now()
	setText(a.ctl[idStart], "Stop")
	a.setStatus(0, "Starting "+c.Name+"...")
}

func (a *App) stopCamera() {
	if a.cap != nil {
		a.cap.Stop()
		a.cap = nil
	}
	setText(a.ctl[idStart], "Start")
}

func (a *App) fillResCombo(formats []vfmt) {
	cb := a.ctl[idResCombo]
	sendMsg(cb, cbResetContent, 0, 0)
	a.resModes = []resMode{{0, 0}, {-1, -1}}
	sendMsg(cb, cbAddString, 0, uintptr(unsafe.Pointer(u16("Maximum available"))))
	sendMsg(cb, cbAddString, 0, uintptr(unsafe.Pointer(u16("Camera default"))))
	sel := 0
	for _, f := range formats {
		a.resModes = append(a.resModes, resMode{f.W, f.H})
		sendMsg(cb, cbAddString, 0, uintptr(unsafe.Pointer(u16(f.label()))))
	}
	for i, r := range a.resModes {
		if r == a.req {
			sel = i
		}
	}
	sendMsg(cb, cbSetCurSel, uintptr(sel), 0)
}

func (a *App) tick() {
	c := a.cap
	if c == nil {
		return
	}
	if err := c.Err(); err != nil {
		a.stopCamera()
		a.setStatus(0, err.Error())
		return
	}
	formats, act, ready := c.Formats()
	if ready && !a.fmtShown {
		a.fmtShown = true
		a.fillResCombo(formats)
	}
	if a.checked(idFreeze) {
		return
	}
	seq := c.Latest(a.lastSeq, func(w, h int, pix []byte) { a.viewer.setImage(w, h, pix) })
	if seq != a.lastSeq {
		a.frames += int(seq - a.lastSeq)
		a.lastSeq = seq
	}
	if dt := time.Since(a.fpsT).Seconds(); dt >= 1 {
		a.fps = float64(a.frames) / dt
		a.frames, a.fpsT = 0, time.Now()
		if a.viewer.hasImg() {
			a.setStatus(0, fmt.Sprintf("%s live - %dx%d %s, %.1f fps", c.Name, a.viewer.imgW, a.viewer.imgH, act.Sub, a.fps))
		}
	}
}

// ---- files

func (a *App) openImage() {
	path, _ := fileDialog(a.hwnd, false, "Open image", "Images|*.png;*.jpg;*.jpeg;*.bmp;*.tif;*.tiff;*.gif|All files|*.*", "", "")
	if path == "" {
		return
	}
	f, err := os.Open(path)
	if err != nil {
		msgBox(a.hwnd, err.Error(), "Open image", mbIconError)
		return
	}
	img, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		msgBox(a.hwnd, "Could not read image: "+err.Error(), "Open image", mbIconError)
		return
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	pix := make([]byte, w*h*4)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := (y*w + x) * 4
			pix[i], pix[i+1], pix[i+2], pix[i+3] = byte(bl>>8), byte(g>>8), byte(r>>8), 255
		}
	}
	sendMsg(a.ctl[idFreeze], bmSetCheck, 1, 0)
	a.viewer.setImage(w, h, pix)
	a.setStatus(0, fmt.Sprintf("%s - %dx%d (frozen; uncheck Freeze for live)", filepath.Base(path), w, h))
}

func (a *App) saveImage(annotated bool) {
	v := a.viewer
	if !v.hasImg() {
		return
	}
	name := time.Now().Format("microscope_20060102_150405")
	if annotated {
		name += "_annotated"
	}
	path, idx := fileDialog(a.hwnd, true, "Save image", "PNG|*.png|JPEG|*.jpg|TIFF|*.tif", "png", name)
	if path == "" {
		return
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		ext = []string{".png", ".png", ".jpg", ".tif"}[min(idx, 3)]
		path += ext
	}
	img := v.rawImage()
	if annotated {
		img = v.annotatedImage()
	}
	f, err := os.Create(path)
	if err == nil {
		switch ext {
		case ".jpg", ".jpeg":
			err = jpeg.Encode(f, img, &jpeg.Options{Quality: 95})
		case ".tif", ".tiff":
			err = tiff.Encode(f, img, &tiff.Options{Compression: tiff.Deflate})
		default:
			err = png.Encode(f, img)
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		msgBox(a.hwnd, err.Error(), "Save image", mbIconError)
		return
	}
	a.setStatus(0, "Saved "+path)
}

func (a *App) exportCSV() {
	v := a.viewer
	if len(v.shapes) == 0 {
		return
	}
	path, _ := fileDialog(a.hwnd, true, "Export measurements", "CSV|*.csv", "csv", "measurements.csv")
	if path == "" {
		return
	}
	f, err := os.Create(path)
	if err != nil {
		msgBox(a.hwnd, err.Error(), "Export CSV", mbIconError)
		return
	}
	f.Write([]byte{0xEF, 0xBB, 0xBF}) // BOM so Excel reads UTF-8
	w := csv.NewWriter(f)
	w.Write([]string{"#", "type", "value", "details", "pixels_per_mm", "points_px"})
	ppmS := "uncalibrated"
	if v.ppm > 0 {
		ppmS = fmt.Sprintf("%.4f", v.ppm)
	}
	for i, sh := range v.shapes {
		lab, det := sh.Info(v.ppm)
		var pts []string
		for _, p := range sh.Pts {
			pts = append(pts, fmt.Sprintf("(%.2f,%.2f)", p.X, p.Y))
		}
		w.Write([]string{strconv.Itoa(i + 1), typeName[sh.Kind], lab, det, ppmS, strings.Join(pts, ";")})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		msgBox(a.hwnd, err.Error(), "Export CSV", mbIconError)
	}
	f.Close()
	a.setStatus(0, "Exported "+path)
}

// ---- measurement list

func (a *App) refreshList() {
	lb := a.ctl[idList]
	sendMsg(lb, lbResetContent, 0, 0)
	for i, sh := range a.viewer.shapes {
		lab, det := sh.Info(a.viewer.ppm)
		s := fmt.Sprintf("%d %s\t%s", i+1, typeName[sh.Kind], lab)
		if det != "" {
			s += "   (" + det + ")"
		}
		sendMsg(lb, lbAddString, 0, uintptr(unsafe.Pointer(u16(s))))
	}
	a.listSelect(a.viewer.sel)
}

func (a *App) listSelect(i int) {
	if i < 0 || i >= len(a.viewer.shapes) {
		i = -1
	}
	sendMsg(a.ctl[idList], lbSetCurSel, uintptr(i), 0)
}

// ---- calibration

func (a *App) saveCal() {
	if err := saveCalibrations(a.profiles, a.active); err != nil {
		msgBox(a.hwnd, "Could not save calibration file:\n"+err.Error(), "Calibration", mbIconError)
	}
}

func (a *App) refreshProfiles() {
	cb := a.ctl[idProfCombo]
	sendMsg(cb, cbResetContent, 0, 0)
	sendMsg(cb, cbAddString, 0, uintptr(unsafe.Pointer(u16("(none)"))))
	sel := 0
	for i, n := range profileNames(a.profiles) {
		sendMsg(cb, cbAddString, 0, uintptr(unsafe.Pointer(u16(n))))
		if n == a.active {
			sel = i + 1
		}
	}
	sendMsg(cb, cbSetCurSel, uintptr(sel), 0)
}

// updatePPM derives effective pixels/mm for the current image size from the active profile.
func (a *App) updatePPM() {
	v := a.viewer
	prof, ok := a.profiles[a.active]
	var txt string
	a.calOK = false
	if !ok || a.active == "" || prof.W <= 0 || prof.H <= 0 {
		v.ppm = 0
		txt = "Not calibrated - values are in pixels."
	} else {
		iw, ih := prof.W, prof.H
		if v.hasImg() {
			iw, ih = v.imgW, v.imgH
		}
		v.ppm = prof.PPM * float64(iw) / float64(prof.W)
		txt = fmt.Sprintf("'%s': %.2f px/mm  (%.2f µm/px)\r\ncalibrated at %dx%d, current %dx%d",
			a.active, v.ppm, 1000/v.ppm, prof.W, prof.H, iw, ih)
		if math.Abs(float64(iw)/float64(ih)-float64(prof.W)/float64(prof.H)) > 0.02 {
			txt = fmt.Sprintf("'%s' - aspect ratio differs from calibration (%dx%d vs %dx%d): recalibrate at this resolution!",
				a.active, prof.W, prof.H, iw, ih)
		} else {
			a.calOK = true
		}
	}
	setText(a.ctl[idCalStatus], txt)
	invalidate(a.ctl[idCalStatus])
	a.refreshList()
	v.redraw()
}

func (a *App) knownMM() float64 {
	f, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(getText(a.ctl[idKnown])), ",", "."), 64)
	if err != nil {
		return 0
	}
	return f
}

func (a *App) updateReadout() {
	c := a.viewer.cal
	if c == nil {
		setText(a.ctl[idReadout], "Measured: - (draw a calibration line)")
		return
	}
	L := dist(c.Pts[0], c.Pts[1])
	if k := a.knownMM(); k > 0 && L > 0 {
		setText(a.ctl[idReadout], fmt.Sprintf("Line %.2f px  =>  %.2f px/mm  (%.2f µm/px)", L, L/k, 1000*k/L))
	} else {
		setText(a.ctl[idReadout], fmt.Sprintf("Line %.2f px", L))
	}
}

func (a *App) applyCalibration() {
	v := a.viewer
	k := a.knownMM()
	switch {
	case !v.hasImg():
		msgBox(a.hwnd, "No image available.", "Calibration", mbIconInfo)
		return
	case v.cal == nil:
		msgBox(a.hwnd, "Draw a calibration line across the ruler first (tool K).", "Calibration", mbIconInfo)
		return
	case k <= 0:
		msgBox(a.hwnd, "Enter a valid known distance in mm.", "Calibration", mbIconInfo)
		return
	}
	name := strings.TrimSpace(getText(a.ctl[idName]))
	if name == "" {
		name = "Calibration"
	}
	if _, exists := a.profiles[name]; exists && !selfTesting &&
		msgBox(a.hwnd, fmt.Sprintf("Overwrite calibration '%s'?", name), "Calibration", mbYesNo|mbIconQuestion) != idYes {
		return
	}
	L := dist(v.cal.Pts[0], v.cal.Pts[1])
	a.profiles[name] = Profile{PPM: L / k, W: v.imgW, H: v.imgH, KnownMM: k, LinePx: L,
		Date: time.Now().Format("2006-01-02T15:04:05")}
	a.active = name
	a.saveCal()
	a.refreshProfiles()
	v.clearCal()
	a.updatePPM()
	a.setTool("line")
	a.setStatus(0, fmt.Sprintf("Calibration '%s' saved: %.2f px/mm. Verify by measuring a ruler span.", name, L/k))
}
