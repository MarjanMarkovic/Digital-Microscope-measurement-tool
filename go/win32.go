package main

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	comctl32 = windows.NewLazySystemDLL("comctl32.dll")
	comdlg32 = windows.NewLazySystemDLL("comdlg32.dll")
	ole32    = windows.NewLazySystemDLL("ole32.dll")

	pRegisterClassExW     = user32.NewProc("RegisterClassExW")
	pCreateWindowExW      = user32.NewProc("CreateWindowExW")
	pDefWindowProcW       = user32.NewProc("DefWindowProcW")
	pDestroyWindow        = user32.NewProc("DestroyWindow")
	pGetMessageW          = user32.NewProc("GetMessageW")
	pTranslateMessage     = user32.NewProc("TranslateMessage")
	pDispatchMessageW     = user32.NewProc("DispatchMessageW")
	pPostQuitMessage      = user32.NewProc("PostQuitMessage")
	pPostMessageW         = user32.NewProc("PostMessageW")
	pShowWindow           = user32.NewProc("ShowWindow")
	pUpdateWindow         = user32.NewProc("UpdateWindow")
	pMoveWindow           = user32.NewProc("MoveWindow")
	pSetWindowTextW       = user32.NewProc("SetWindowTextW")
	pGetWindowTextW       = user32.NewProc("GetWindowTextW")
	pGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	pSendMessageW         = user32.NewProc("SendMessageW")
	pInvalidateRect       = user32.NewProc("InvalidateRect")
	pBeginPaint           = user32.NewProc("BeginPaint")
	pEndPaint             = user32.NewProc("EndPaint")
	pGetClientRect        = user32.NewProc("GetClientRect")
	pGetWindowRect        = user32.NewProc("GetWindowRect")
	pSetTimer             = user32.NewProc("SetTimer")
	pLoadCursorW          = user32.NewProc("LoadCursorW")
	pLoadImageW           = user32.NewProc("LoadImageW")
	pSetCursor            = user32.NewProc("SetCursor")
	pSetCapture           = user32.NewProc("SetCapture")
	pReleaseCapture       = user32.NewProc("ReleaseCapture")
	pGetFocus             = user32.NewProc("GetFocus")
	pSetFocus             = user32.NewProc("SetFocus")
	pGetClassNameW        = user32.NewProc("GetClassNameW")
	pMessageBoxW          = user32.NewProc("MessageBoxW")
	pScreenToClient       = user32.NewProc("ScreenToClient")
	pGetDpiForSystem      = user32.NewProc("GetDpiForSystem")
	pGetKeyState          = user32.NewProc("GetKeyState")
	pFillRect             = user32.NewProc("FillRect")
	pGetSysColorBrush     = user32.NewProc("GetSysColorBrush")
	pEnableWindow         = user32.NewProc("EnableWindow")
	pDrawEdge             = user32.NewProc("DrawEdge")
	pGetSysColor          = user32.NewProc("GetSysColor")

	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pCreateDIBSection       = gdi32.NewProc("CreateDIBSection")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pDeleteDC               = gdi32.NewProc("DeleteDC")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pStretchBlt             = gdi32.NewProc("StretchBlt")
	pSetStretchBltMode      = gdi32.NewProc("SetStretchBltMode")
	pSetBrushOrgEx          = gdi32.NewProc("SetBrushOrgEx")
	pCreatePen              = gdi32.NewProc("CreatePen")
	pCreateSolidBrush       = gdi32.NewProc("CreateSolidBrush")
	pGetStockObject         = gdi32.NewProc("GetStockObject")
	pMoveToEx               = gdi32.NewProc("MoveToEx")
	pLineTo                 = gdi32.NewProc("LineTo")
	pPolyline               = gdi32.NewProc("Polyline")
	pEllipse                = gdi32.NewProc("Ellipse")
	pRectangle              = gdi32.NewProc("Rectangle")
	pArc                    = gdi32.NewProc("Arc")
	pSetBkMode              = gdi32.NewProc("SetBkMode")
	pSetBkColor             = gdi32.NewProc("SetBkColor")
	pSetTextColor           = gdi32.NewProc("SetTextColor")
	pExtTextOutW            = gdi32.NewProc("ExtTextOutW")
	pGetTextExtentPoint32W  = gdi32.NewProc("GetTextExtentPoint32W")
	pCreateFontW            = gdi32.NewProc("CreateFontW")
	pGdiFlush               = gdi32.NewProc("GdiFlush")

	pGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")

	pInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
	pGetOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
	pGetSaveFileNameW     = comdlg32.NewProc("GetSaveFileNameW")

	pCoInitializeEx = ole32.NewProc("CoInitializeEx")
	pCoUninitialize = ole32.NewProc("CoUninitialize")
	pCoTaskMemFree  = ole32.NewProc("CoTaskMemFree")
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	wsTabStop          = 0x00010000
	wsVScroll          = 0x00200000
	wsClipChildren     = 0x02000000
	wsGroup            = 0x00020000
	wsExClientEdge     = 0x00000200

	bsPushButton   = 0x0
	bsAutoCheckbox = 0x3
	bsRadioButton  = 0x4
	bsGroupBox     = 0x7

	cbsDropDownList = 0x3
	cbsHasStrings   = 0x200
	cbAddString     = 0x143
	cbResetContent  = 0x14B
	cbSetCurSel     = 0x14E
	cbGetCurSel     = 0x147
	cbnSelChange    = 1

	lbsNotify           = 0x1
	lbsUseTabStops      = 0x80
	lbsNoIntegralHeight = 0x100
	lbAddString         = 0x180
	lbResetContent      = 0x184
	lbSetCurSel         = 0x186
	lbGetCurSel         = 0x188
	lbSetTabStops       = 0x192
	lbnSelChange        = 1

	bmGetCheck = 0xF0
	bmSetCheck = 0xF1
	bnClicked  = 0

	esAutoHScroll = 0x80
	enChange      = 0x300
	ssNoPrefix    = 0x80

	wmCreate         = 0x0001
	wmDestroy        = 0x0002
	wmSize           = 0x0005
	wmPaint          = 0x000F
	wmClose          = 0x0010
	wmEraseBkgnd     = 0x0014
	wmSetCursor      = 0x0020
	wmGetMinMaxInfo  = 0x0024
	wmSetFont        = 0x0030
	wmKeyDown        = 0x0100
	wmCommand        = 0x0111
	wmTimer          = 0x0113
	wmCtlColorStatic = 0x0138
	wmMouseMove      = 0x0200
	wmLButtonDown    = 0x0201
	wmLButtonUp      = 0x0202
	wmLButtonDblClk  = 0x0203
	wmRButtonDown    = 0x0204
	wmMButtonDown    = 0x0207
	wmMButtonUp      = 0x0208
	wmMouseWheel     = 0x020A
	wmCaptureChanged = 0x0215
	wmUser           = 0x0400

	sbSetParts = wmUser + 4
	sbSetTextW = wmUser + 11

	csVRedraw  = 0x1
	csHRedraw  = 0x2
	csDblClks  = 0x8
	idcArrow   = 32512
	idcCross   = 32515
	idcWait    = 32514
	htClient   = 1
	mkLButton  = 0x1
	swMaximize = 3

	colorBtnFace = 15
	colorBtnText = 18
	edgeEtched   = 0x6
	bfRect       = 0xF

	mbOK           = 0x0
	mbYesNo        = 0x4
	mbIconError    = 0x10
	mbIconQuestion = 0x20
	mbIconInfo     = 0x40
	idYes          = 6

	psSolid       = 0
	nullBrush     = 5
	bkTransparent = 1
	bkOpaque      = 2
	etoOpaque     = 0x2
	srcCopy       = 0x00CC0020
	halftone      = 4
	colorOnColor  = 3

	vkBack    = 0x08
	vkReturn  = 0x0D
	vkEscape  = 0x1B
	vkDelete  = 0x2E
	vkControl = 0x11
)

type point struct{ X, Y int32 }
type rect struct{ Left, Top, Right, Bottom int32 }
type size struct{ CX, CY int32 }

type msgT struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      point
	Private uint32
}

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type paintStruct struct {
	Hdc       uintptr
	Erase     int32
	Paint     rect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type openFileName struct {
	StructSize    uint32
	Owner         uintptr
	Instance      uintptr
	Filter        *uint16
	CustomFilter  *uint16
	MaxCustFilter uint32
	FilterIndex   uint32
	File          *uint16
	MaxFile       uint32
	FileTitle     *uint16
	MaxFileTitle  uint32
	InitialDir    *uint16
	Title         *uint16
	Flags         uint32
	FileOffset    uint16
	FileExtension uint16
	DefExt        *uint16
	CustData      uintptr
	Hook          uintptr
	TemplateName  *uint16
	PvReserved    uintptr
	DwReserved    uint32
	FlagsEx       uint32
}

type minMaxInfo struct {
	Reserved, MaxSize, MaxPosition, MinTrackSize, MaxTrackSize point
}

type initCommonControlsEx struct{ Size, ICC uint32 }

func u16(s string) *uint16 {
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		p, _ = windows.UTF16PtrFromString("?")
	}
	return p
}

func loWord(x uintptr) int { return int(int16(x & 0xffff)) }
func hiWord(x uintptr) int { return int(int16((x >> 16) & 0xffff)) }

func rgb(r, g, b uint8) uint32 { return uint32(r) | uint32(g)<<8 | uint32(b)<<16 }

func sendMsg(h uintptr, msg uint32, w, l uintptr) uintptr {
	r, _, _ := pSendMessageW.Call(h, uintptr(msg), w, l)
	return r
}

func setText(h uintptr, s string) {
	pSetWindowTextW.Call(h, uintptr(unsafe.Pointer(u16(s))))
}

func getText(h uintptr) string {
	n, _, _ := pGetWindowTextLengthW.Call(h)
	buf := make([]uint16, n+1)
	pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}

func className(h uintptr) string {
	buf := make([]uint16, 64)
	pGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 64)
	return syscall.UTF16ToString(buf)
}

func msgBox(owner uintptr, text, caption string, flags uint32) int {
	r, _, _ := pMessageBoxW.Call(owner, uintptr(unsafe.Pointer(u16(text))), uintptr(unsafe.Pointer(u16(caption))), uintptr(flags))
	return int(r)
}

func invalidate(h uintptr) { pInvalidateRect.Call(h, 0, 0) }

func clientSize(h uintptr) (int, int) {
	var r rect
	pGetClientRect.Call(h, uintptr(unsafe.Pointer(&r)))
	return int(r.Right - r.Left), int(r.Bottom - r.Top)
}

// createDIB makes a top-down 32bpp BGRX DIB section and returns its handle and pixel slice.
func createDIB(w, h int) (uintptr, []byte) {
	bi := bitmapInfoHeader{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	var bits uintptr
	bmp, _, _ := pCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == 0 {
		return 0, nil
	}
	return bmp, unsafe.Slice((*byte)(unsafe.Pointer(bits)), w*h*4)
}

// fileDialog shows an open/save dialog. filter uses '|' as separator, e.g. "PNG|*.png|All|*.*".
func fileDialog(owner uintptr, save bool, title, filter, defExt, initial string) (string, int) {
	f := []uint16{}
	for _, part := range splitBar(filter) {
		f = append(f, windows.StringToUTF16(part)...)
	}
	f = append(f, 0)
	buf := make([]uint16, 1024)
	copy(buf, windows.StringToUTF16(initial))
	ofn := openFileName{
		StructSize:  uint32(unsafe.Sizeof(openFileName{})),
		Owner:       owner,
		Filter:      &f[0],
		FilterIndex: 1,
		File:        &buf[0],
		MaxFile:     uint32(len(buf)),
		Title:       u16(title),
	}
	if defExt != "" {
		ofn.DefExt = u16(defExt)
	}
	const ofnOverwritePrompt, ofnFileMustExist, ofnPathMustExist, ofnNoChangeDir = 0x2, 0x1000, 0x800, 0x8
	var r uintptr
	if save {
		ofn.Flags = ofnOverwritePrompt | ofnPathMustExist | ofnNoChangeDir
		r, _, _ = pGetSaveFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	} else {
		ofn.Flags = ofnFileMustExist | ofnPathMustExist | ofnNoChangeDir
		r, _, _ = pGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	}
	if r == 0 {
		return "", 0
	}
	return syscall.UTF16ToString(buf), int(ofn.FilterIndex)
}

func splitBar(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '|' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
