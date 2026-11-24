//go:build windows

package profile

// qrscreen_windows.go — "Scan QR from screen" (issue #102, v2rayN parity).
//
// v2rayN imports subscriptions by scanning the whole screen for a QR code
// (no camera needed on desktop). This is the Windows half: one GDI pass
// captures the virtual screen (all monitors) into an RGBA image via pure
// syscall (gdi32/user32 through golang.org/x/sys/windows LazyDLL — no cgo,
// so CGO_ENABLED=0 cross-builds keep working), then the pure-Go ZXing port
// (qrdecode.go) reads the first QR code in it.

import (
	"fmt"
	"image"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procGetDC                  = user32.NewProc("GetDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
)

// GetSystemMetrics indexes for the virtual screen.
const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
)

const srccopy = 0x00CC0020

// dibHeader mirrors BITMAPINFOHEADER for a top-down 32-bpp read.
type dibHeader struct {
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

func sm(idx int) int {
	v, _, _ := procGetSystemMetrics.Call(uintptr(idx))
	return int(v)
}

// CaptureScreenQR scans the whole virtual screen (all monitors) for a QR
// code and returns its text.
func CaptureScreenQR() (string, error) {
	img, err := captureVirtualScreen()
	if err != nil {
		return "", err
	}
	return DecodeQR(img)
}

// captureVirtualScreen blits the virtual screen into an RGBA image.
func captureVirtualScreen() (image.Image, error) {
	x, y := sm(smXVirtualScreen), sm(smYVirtualScreen)
	w, h := sm(smCXVirtualScreen), sm(smCYVirtualScreen)
	if w <= 0 || h <= 0 || w > 32768 || h > 32768 {
		return nil, fmt.Errorf("screen capture: invalid virtual screen %dx%d", w, h)
	}
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return nil, fmt.Errorf("screen capture: GetDC failed")
	}
	defer procReleaseDC.Call(0, hdc)

	memDC, _, _ := procCreateCompatibleDC.Call(hdc)
	if memDC == 0 {
		return nil, fmt.Errorf("screen capture: CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(memDC)

	bmp, _, _ := procCreateCompatibleBitmap.Call(hdc, uintptr(w), uintptr(h))
	if bmp == 0 {
		return nil, fmt.Errorf("screen capture: CreateCompatibleBitmap failed")
	}
	defer procDeleteObject.Call(bmp)

	old, _, _ := procSelectObject.Call(memDC, bmp)
	defer procSelectObject.Call(memDC, old)

	ok, _, _ := procBitBlt.Call(memDC, 0, 0, uintptr(w), uintptr(h), hdc, uintptr(x), uintptr(y), srccopy)
	if ok == 0 {
		return nil, fmt.Errorf("screen capture: BitBlt failed")
	}

	// Top-down 32-bpp DIB (negative height) so rows come out in reading
	// order; GetDIBits fills a BGRA buffer which flips to RGBA below.
	bi := dibHeader{
		Size:     uint32(unsafe.Sizeof(dibHeader{})),
		Width:    int32(w),
		Height:   -int32(h), // top-down
		Planes:   1,
		BitCount: 32,
	}
	buf := make([]byte, w*h*4)
	got, _, _ := procGetDIBits.Call(hdc, bmp, 0, uintptr(h),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), 0 /* DIB_RGB_COLORS */)
	if got == 0 {
		return nil, fmt.Errorf("screen capture: GetDIBits failed")
	}

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for row := 0; row < h; row++ {
		src := row * w * 4
		dst := img.PixOffset(0, row)
		for col := 0; col < w; col++ {
			b, g, r := buf[src+2], buf[src+1], buf[src]
			img.Pix[dst] = r
			img.Pix[dst+1] = g
			img.Pix[dst+2] = b
			img.Pix[dst+3] = 0xFF
			src += 4
			dst += 4
		}
	}
	return img, nil
}
