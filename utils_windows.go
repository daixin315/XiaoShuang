//go:build windows

package main

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"unsafe"
)

// hideWindow 隐藏子进程控制台窗口（Windows：GUI 程序 exec 控制台子进程会弹黑窗）
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// setupLogFile Windows 下把日志写进 exe 同目录 log.txt（GUI 无控制台，方便排查）
func setupLogFile() {
	f, err := os.OpenFile(filepath.Join(exeDir, "log.txt"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		os.Stdout = f
		os.Stderr = f
	}
}

// captureScreen Windows 原生截全屏（GDI），保存 PNG
func captureScreen(path string) error {
	user32 := syscall.NewLazyDLL("user32.dll")
	gdi32 := syscall.NewLazyDLL("gdi32.dll")
	getDC := user32.NewProc("GetDC")
	releaseDC := user32.NewProc("ReleaseDC")
	getSysMetrics := user32.NewProc("GetSystemMetrics")
	createCompatDC := gdi32.NewProc("CreateCompatibleDC")
	createCompatBmp := gdi32.NewProc("CreateCompatibleBitmap")
	selectObj := gdi32.NewProc("SelectObject")
	bitBlt := gdi32.NewProc("BitBlt")
	getDIBits := gdi32.NewProc("GetDIBits")
	deleteObj := gdi32.NewProc("DeleteObject")
	deleteDC := gdi32.NewProc("DeleteDC")

	// SM_CXSCREEN=0 SM_CYSCREEN=1
	w, _, _ := getSysMetrics.Call(0)
	h, _, _ := getSysMetrics.Call(1)
	if w == 0 || h == 0 {
		return errScreenZero
	}
	screenDC, _, _ := getDC.Call(0)
	if screenDC == 0 {
		return errScreenDC
	}
	defer releaseDC.Call(0, screenDC)

	memDC, _, _ := createCompatDC.Call(screenDC)
	if memDC == 0 {
		return errScreenDC
	}
	defer deleteDC.Call(memDC)

	hBmp, _, _ := createCompatBmp.Call(screenDC, w, h)
	if hBmp == 0 {
		return errScreenDC
	}
	defer deleteObj.Call(hBmp)
	selectObj.Call(memDC, hBmp)
	// SRCCOPY
	bitBlt.Call(memDC, 0, 0, w, h, screenDC, 0, 0, 0x00CC0020)

	// 取像素：GetDIBits 到 BGRA buffer
	bmi := struct {
		Size         uint32
		Width        int32
		Height       int32
		Planes       uint16
		BitCount     uint16
		Compression  uint32
		SizeImage    uint32
		Xpels        int32
		Ypels        int32
		ClrUsed      uint32
		ClrImportant uint32
	}{}
	bmi.Size = uint32(unsafe.Sizeof(bmi))
	bmi.Width = int32(w)
	bmi.Height = -int32(h) // 负值 = 自顶向下
	bmi.Planes = 1
	bmi.BitCount = 32

	pix := make([]byte, w*h*4)
	if r, _, _ := getDIBits.Call(screenDC, hBmp, 0, uintptr(h), uintptr(unsafe.Pointer(&pix[0])),
		uintptr(unsafe.Pointer(&bmi)), 0); r == 0 {
		return errScreenDC
	}

	// BGRA → RGBA
	img := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	for y := 0; y < int(h); y++ {
		for x := 0; x < int(w); x++ {
			i := (y*int(w) + x) * 4
			img.SetRGBA(x, y, color.RGBA{R: pix[i+2], G: pix[i+1], B: pix[i], A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

var (
	errScreenZero = syscall.Errno(1)
	errScreenDC   = syscall.Errno(2)
)
