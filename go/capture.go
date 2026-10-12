package main

// Webcam (UVC) capture through Media Foundation, called via raw COM vtables (no cgo).

import (
	"fmt"
	"runtime"
	"sort"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	mfplat = windows.NewLazySystemDLL("mfplat.dll")
	mfdll  = windows.NewLazySystemDLL("mf.dll")
	mfrw   = windows.NewLazySystemDLL("mfreadwrite.dll")

	pMFStartup                           = mfplat.NewProc("MFStartup")
	pMFCreateAttributes                  = mfplat.NewProc("MFCreateAttributes")
	pMFCreateMediaType                   = mfplat.NewProc("MFCreateMediaType")
	pMFEnumDeviceSources                 = mfdll.NewProc("MFEnumDeviceSources")
	pMFCreateSourceReaderFromMediaSource = mfrw.NewProc("MFCreateSourceReaderFromMediaSource")
)

func guid(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

var (
	guidSourceType      = guid("{c60ac5fe-252a-478f-a0ef-bc8fa5f7cad3}")
	guidSourceVidcap    = guid("{8ac3587a-4ae7-42d8-99e0-0a6013eef90f}")
	guidFriendlyName    = guid("{60d0e559-52f8-4fa2-bbce-acdb34a8ec01}")
	guidSymbolicLink    = guid("{58f0aad8-22bf-4f8a-bb3d-d2c4978c6e2f}")
	iidIMFMediaSource   = guid("{279a808d-aec7-40c8-9c6b-a6b492c78a66}")
	iidIMF2DBuffer      = guid("{7DC9D5F9-9ED9-44ec-9BBF-0600BB589FBB}")
	guidAdvancedVP      = guid("{0f81da2c-b537-4672-a8b2-a681b17307a3}")
	guidVideoProcessing = guid("{fb394f3d-ccf1-42ee-bbb3-f9b845d5681d}")
	guidMajorType       = guid("{48eba18e-f8c9-4687-bf11-0a74c9f96a8f}")
	guidSubtype         = guid("{f7e34c9a-42e8-4714-b74b-cb29d72c35e5}")
	guidFrameSize       = guid("{1652c33d-d6b2-4012-b834-72030849a37d}")
	guidFrameRate       = guid("{c459a2e8-3d2c-4e44-b132-fee5156c7bb0}")
	guidDefaultStride   = guid("{644b4e48-1e02-4516-b0eb-c01ca9d49ac6}")
	guidMediaTypeVideo  = guid("{73646976-0000-0010-8000-00AA00389B71}")
	guidFmtRGB32        = guid("{00000016-0000-0010-8000-00AA00389B71}")
	guidFmtNV12         = guid("{3231564E-0000-0010-8000-00AA00389B71}")
	guidFmtYUY2         = guid("{32595559-0000-0010-8000-00AA00389B71}")
)

const (
	mfVersion          = 0x00020070
	firstVideoStream   = 0xFFFFFFFC
	allStreams         = 0xFFFFFFFE
	readerfError       = 0x1
	readerfEndOfStream = 0x2
	readerfTypeChanged = 0x20

	// IMFAttributes vtable slots
	vtGetUINT32          = 7
	vtGetUINT64          = 8
	vtGetGUID            = 10
	vtGetAllocatedString = 13
	vtSetUINT32          = 21
	vtSetGUID            = 24
)

//go:uintptrescapes
func vcall(obj uintptr, idx int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(idx)*unsafe.Sizeof(uintptr(0))))
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, obj)
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(fn, all...)
	return r
}

func release(obj uintptr) {
	if obj != 0 {
		vcall(obj, 2)
	}
}

func hrFailed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

func hrErr(what string, hr uintptr) error { return fmt.Errorf("%s failed (0x%08X)", what, uint32(hr)) }

func mfStartup() error {
	hr, _, _ := pMFStartup.Call(mfVersion, 0)
	if hrFailed(hr) {
		return hrErr("MFStartup", hr)
	}
	return nil
}

func comInitMTA() { pCoInitializeEx.Call(0, 0) }

func attrString(obj uintptr, key *windows.GUID) string {
	var p uintptr
	var n uint32
	if hrFailed(vcall(obj, vtGetAllocatedString, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&n)))) || p == 0 {
		return ""
	}
	s := windows.UTF16PtrToString((*uint16)(unsafe.Pointer(p)))
	pCoTaskMemFree.Call(p)
	return s
}

func attrU64(obj uintptr, key *windows.GUID) (uint64, bool) {
	var v uint64
	ok := !hrFailed(vcall(obj, vtGetUINT64, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&v))))
	return v, ok
}

func attrGUID(obj uintptr, key *windows.GUID) windows.GUID {
	var g windows.GUID
	vcall(obj, vtGetGUID, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&g)))
	return g
}

// enumActivates returns IMFActivate objects for all video capture devices (caller releases).
func enumActivates() ([]uintptr, error) {
	var attr uintptr
	if hr, _, _ := pMFCreateAttributes.Call(uintptr(unsafe.Pointer(&attr)), 1); hrFailed(hr) {
		return nil, hrErr("MFCreateAttributes", hr)
	}
	defer release(attr)
	vcall(attr, vtSetGUID, uintptr(unsafe.Pointer(&guidSourceType)), uintptr(unsafe.Pointer(&guidSourceVidcap)))
	var arr uintptr
	var n uint32
	if hr, _, _ := pMFEnumDeviceSources.Call(attr, uintptr(unsafe.Pointer(&arr)), uintptr(unsafe.Pointer(&n))); hrFailed(hr) {
		return nil, hrErr("MFEnumDeviceSources", hr)
	}
	list := make([]uintptr, n)
	if n > 0 {
		copy(list, unsafe.Slice((*uintptr)(unsafe.Pointer(arr)), n))
	}
	pCoTaskMemFree.Call(arr)
	return list, nil
}

type camDev struct{ Name, Link string }

func listCameras() ([]camDev, error) {
	acts, err := enumActivates()
	if err != nil {
		return nil, err
	}
	var out []camDev
	seen := map[string]int{}
	for _, a := range acts {
		name := attrString(a, &guidFriendlyName)
		if name == "" {
			name = "Camera"
		}
		seen[name]++
		if seen[name] > 1 {
			name = fmt.Sprintf("%s (%d)", name, seen[name])
		}
		out = append(out, camDev{name, attrString(a, &guidSymbolicLink)})
		release(a)
	}
	return out, nil
}

// vfmt is one native camera mode.
type vfmt struct {
	idx  int
	W, H int
	FPS  float64
	Sub  string
}

func (f vfmt) label() string { return fmt.Sprintf("%dx%d  (%.0f fps %s)", f.W, f.H, f.FPS, f.Sub) }

func subName(g windows.GUID) string {
	if g.Data2 == 0 && g.Data3 == 0x10 {
		if g.Data1 < 256 {
			switch g.Data1 {
			case 22:
				return "RGB32"
			case 20:
				return "RGB24"
			}
			return fmt.Sprintf("D3D%d", g.Data1)
		}
		b := []byte{byte(g.Data1), byte(g.Data1 >> 8), byte(g.Data1 >> 16), byte(g.Data1 >> 24)}
		return string(b)
	}
	return "?"
}

// Capture runs a camera on its own OS thread and keeps the newest frame (BGRX).
type Capture struct {
	Link     string
	Name     string
	ReqW     int // >0: requested size; 0: maximum; -1: camera default
	ReqH     int
	mu       sync.Mutex
	frame    []byte
	spare    []byte
	w, h     int
	seq      uint64
	err      error
	formats  []vfmt // one best mode per size, largest first
	fmtReady bool
	active   vfmt
	stop     chan struct{}
	done     chan struct{}
}

func startCapture(link, name string, reqW, reqH int) *Capture {
	c := &Capture{Link: link, Name: name, ReqW: reqW, ReqH: reqH, stop: make(chan struct{}), done: make(chan struct{})}
	go c.run()
	return c
}

func (c *Capture) Stop() {
	select {
	case <-c.stop:
	default:
		close(c.stop)
	}
	<-c.done
}

func (c *Capture) fail(err error) {
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

// Err returns the capture error, if any.
func (c *Capture) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Formats returns the available modes once known.
func (c *Capture) Formats() ([]vfmt, vfmt, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.formats, c.active, c.fmtReady
}

// Latest calls fn with the newest frame if it is newer than lastSeq.
func (c *Capture) Latest(lastSeq uint64, fn func(w, h int, pix []byte)) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seq == lastSeq || c.frame == nil {
		return lastSeq
	}
	fn(c.w, c.h, c.frame)
	return c.seq
}

func (c *Capture) run() {
	defer close(c.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	comInitMTA()
	defer pCoUninitialize.Call()

	acts, err := enumActivates()
	if err != nil {
		c.fail(err)
		return
	}
	var act uintptr
	for _, a := range acts {
		if act == 0 && attrString(a, &guidSymbolicLink) == c.Link {
			act = a
			continue
		}
		release(a)
	}
	if act == 0 {
		c.fail(fmt.Errorf("camera %q not found (unplugged?)", c.Name))
		return
	}
	defer release(act)
	var src uintptr
	if hr := vcall(act, 33, uintptr(unsafe.Pointer(&iidIMFMediaSource)), uintptr(unsafe.Pointer(&src))); hrFailed(hr) {
		c.fail(hrErr("Opening camera (in use by another program?)", hr))
		return
	}
	defer func() {
		vcall(src, 12) // IMFMediaSource::Shutdown
		release(src)
		vcall(act, 34) // IMFActivate::ShutdownObject
	}()

	reader, err := createReader(src)
	if err != nil {
		c.fail(err)
		return
	}
	defer release(reader)
	vcall(reader, 4, allStreams, 0)
	vcall(reader, 4, firstVideoStream, 1)

	// enumerate native modes
	var all []vfmt
	for i := 0; ; i++ {
		var mt uintptr
		if hrFailed(vcall(reader, 5, firstVideoStream, uintptr(i), uintptr(unsafe.Pointer(&mt)))) {
			break
		}
		fs, _ := attrU64(mt, &guidFrameSize)
		fr, _ := attrU64(mt, &guidFrameRate)
		f := vfmt{idx: i, W: int(fs >> 32), H: int(fs & 0xffffffff), Sub: subName(attrGUID(mt, &guidSubtype))}
		if den := fr & 0xffffffff; den != 0 {
			f.FPS = float64(fr>>32) / float64(den)
		}
		release(mt)
		if f.W > 0 && f.H > 0 {
			all = append(all, f)
		}
	}
	best := map[[2]int]vfmt{}
	for _, f := range all {
		k := [2]int{f.W, f.H}
		b, ok := best[k]
		if !ok || f.FPS > b.FPS+0.5 || (f.FPS > b.FPS-0.5 && f.Sub == "MJPG" && b.Sub != "MJPG") {
			best[k] = f
		}
	}
	var formats []vfmt
	for _, f := range best {
		formats = append(formats, f)
	}
	sort.Slice(formats, func(i, j int) bool {
		if formats[i].W*formats[i].H != formats[j].W*formats[j].H {
			return formats[i].W*formats[i].H > formats[j].W*formats[j].H
		}
		return formats[i].W > formats[j].W
	})

	var chosen *vfmt
	switch {
	case c.ReqW > 0:
		for i := range formats {
			if formats[i].W == c.ReqW && formats[i].H == c.ReqH {
				chosen = &formats[i]
			}
		}
	case c.ReqW == 0 && len(formats) > 0:
		chosen = &formats[0]
	}
	if chosen != nil {
		var mt uintptr
		if !hrFailed(vcall(reader, 5, firstVideoStream, uintptr(chosen.idx), uintptr(unsafe.Pointer(&mt)))) {
			vcall(reader, 7, firstVideoStream, 0, mt)
			release(mt)
		}
	}

	// ask for an uncompressed output we can convert
	outSub := ""
	for _, cand := range []struct {
		g    *windows.GUID
		name string
	}{{&guidFmtRGB32, "RGB32"}, {&guidFmtNV12, "NV12"}, {&guidFmtYUY2, "YUY2"}} {
		var mt uintptr
		if hr, _, _ := pMFCreateMediaType.Call(uintptr(unsafe.Pointer(&mt))); hrFailed(hr) {
			continue
		}
		vcall(mt, vtSetGUID, uintptr(unsafe.Pointer(&guidMajorType)), uintptr(unsafe.Pointer(&guidMediaTypeVideo)))
		vcall(mt, vtSetGUID, uintptr(unsafe.Pointer(&guidSubtype)), uintptr(unsafe.Pointer(cand.g)))
		hr := vcall(reader, 7, firstVideoStream, 0, mt)
		release(mt)
		if !hrFailed(hr) {
			outSub = cand.name
			break
		}
	}
	if outSub == "" {
		c.fail(fmt.Errorf("camera format not supported (no RGB32/NV12/YUY2 conversion available)"))
		return
	}

	w, h, stride := currentType(reader)
	act0 := vfmt{W: w, H: h, Sub: outSub}
	if chosen != nil {
		act0 = *chosen
	}
	c.mu.Lock()
	c.formats, c.active, c.fmtReady = formats, act0, true
	c.mu.Unlock()

	fails := 0
	for {
		select {
		case <-c.stop:
			return
		default:
		}
		var streamIdx, flags uint32
		var ts int64
		var sample uintptr
		hr := vcall(reader, 9, firstVideoStream, 0, uintptr(unsafe.Pointer(&streamIdx)),
			uintptr(unsafe.Pointer(&flags)), uintptr(unsafe.Pointer(&ts)), uintptr(unsafe.Pointer(&sample)))
		if hrFailed(hr) || flags&readerfError != 0 {
			fails++
			if sample != 0 {
				release(sample)
			}
			if fails > 20 {
				c.fail(hrErr("Camera stopped delivering frames; ReadSample", hr))
				return
			}
			continue
		}
		if flags&readerfEndOfStream != 0 {
			c.fail(fmt.Errorf("camera stream ended (unplugged?)"))
			return
		}
		if flags&readerfTypeChanged != 0 {
			w, h, stride = currentType(reader)
		}
		if sample == 0 {
			continue
		}
		fails = 0
		c.consume(sample, w, h, stride, outSub)
		release(sample)
	}
}

func createReader(src uintptr) (uintptr, error) {
	var lastHR uintptr
	for _, key := range []*windows.GUID{&guidAdvancedVP, &guidVideoProcessing} {
		var attr uintptr
		pMFCreateAttributes.Call(uintptr(unsafe.Pointer(&attr)), 1)
		vcall(attr, vtSetUINT32, uintptr(unsafe.Pointer(key)), 1)
		var reader uintptr
		hr, _, _ := pMFCreateSourceReaderFromMediaSource.Call(src, attr, uintptr(unsafe.Pointer(&reader)))
		release(attr)
		if !hrFailed(hr) {
			return reader, nil
		}
		lastHR = hr
	}
	return 0, hrErr("MFCreateSourceReaderFromMediaSource", lastHR)
}

func currentType(reader uintptr) (w, h, stride int) {
	var mt uintptr
	if hrFailed(vcall(reader, 6, firstVideoStream, uintptr(unsafe.Pointer(&mt)))) {
		return 0, 0, 0
	}
	defer release(mt)
	fs, _ := attrU64(mt, &guidFrameSize)
	w, h = int(fs>>32), int(fs&0xffffffff)
	var s uint32
	if !hrFailed(vcall(mt, vtGetUINT32, uintptr(unsafe.Pointer(&guidDefaultStride)), uintptr(unsafe.Pointer(&s)))) {
		stride = int(int32(s))
	}
	return
}

// consume converts one IMFSample into the BGRX frame buffer.
func (c *Capture) consume(sample uintptr, w, h, stride int, sub string) {
	if w <= 0 || h <= 0 {
		return
	}
	var buf uintptr
	if hrFailed(vcall(sample, 41, uintptr(unsafe.Pointer(&buf)))) || buf == 0 {
		return
	}
	defer release(buf)

	var scan0 uintptr
	var pitch int32
	var b2d uintptr
	if !hrFailed(vcall(buf, 0, uintptr(unsafe.Pointer(&iidIMF2DBuffer)), uintptr(unsafe.Pointer(&b2d)))) && b2d != 0 {
		defer release(b2d)
		if hrFailed(vcall(b2d, 3, uintptr(unsafe.Pointer(&scan0)), uintptr(unsafe.Pointer(&pitch)))) {
			return
		}
		defer vcall(b2d, 4)
	} else {
		var p uintptr
		var maxLen, curLen uint32
		if hrFailed(vcall(buf, 3, uintptr(unsafe.Pointer(&p)), uintptr(unsafe.Pointer(&maxLen)), uintptr(unsafe.Pointer(&curLen)))) {
			return
		}
		defer vcall(buf, 4)
		if stride == 0 {
			stride = w * 4
			if sub != "RGB32" {
				stride = w
				if sub == "YUY2" {
					stride = w * 2
				}
			}
		}
		pitch, scan0 = int32(stride), p
		if stride < 0 {
			scan0 = p + uintptr((h-1)*(-stride))
		}
	}

	row := func(y, length int) []byte {
		return unsafe.Slice((*byte)(unsafe.Pointer(scan0+uintptr(y*int(pitch)))), length)
	}
	n := w * h * 4
	if len(c.spare) != n {
		c.spare = make([]byte, n)
	}
	dst := c.spare
	switch sub {
	case "RGB32":
		for y := 0; y < h; y++ {
			copy(dst[y*w*4:(y+1)*w*4], row(y, w*4))
		}
	case "YUY2":
		for y := 0; y < h; y++ {
			s := row(y, w*2)
			d := dst[y*w*4:]
			for x := 0; x+1 < w; x += 2 {
				y0, u, y1, v := s[x*2], s[x*2+1], s[x*2+2], s[x*2+3]
				yuv(d[x*4:], y0, u, v)
				yuv(d[x*4+4:], y1, u, v)
			}
		}
	case "NV12":
		uvBase := scan0 + uintptr(h*int(pitch))
		for y := 0; y < h; y++ {
			s := row(y, w)
			uv := unsafe.Slice((*byte)(unsafe.Pointer(uvBase+uintptr((y/2)*int(pitch)))), w)
			d := dst[y*w*4:]
			for x := 0; x < w; x++ {
				yuv(d[x*4:], s[x], uv[x&^1], uv[x|1])
			}
		}
	}
	c.mu.Lock()
	c.spare, c.frame = c.frame, dst
	c.w, c.h = w, h
	c.seq++
	c.mu.Unlock()
}

func clamp8(v int) byte {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

// yuv writes one BT.601 (limited range) pixel as BGRX.
func yuv(d []byte, y, u, v byte) {
	C, D, E := int(y)-16, int(u)-128, int(v)-128
	d[0] = clamp8((298*C + 516*D + 128) >> 8)
	d[1] = clamp8((298*C - 100*D - 208*E + 128) >> 8)
	d[2] = clamp8((298*C + 409*E + 128) >> 8)
	d[3] = 255
}
