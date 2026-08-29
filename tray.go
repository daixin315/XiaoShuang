package main

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"

	"fyne.io/fyne/v2"
	"github.com/getlantern/systray"
)

// ===== 系统托盘（关窗保护：点 X 最小化到托盘，托盘右键退出）=====

var (
	trayWindow fyne.Window // 托盘控制的窗口（runMainWindow 里设置）
	trayQuit   bool        // 托盘退出标志（绕过关闭拦截）
)

// startTray 启动系统托盘（goroutine 中运行；Linux 需 appindicator，Ubuntu 预装扩展）
func startTray(w fyne.Window) {
	trayWindow = w
	go systray.Run(trayReady, trayExit)
}

// trayReady 托盘就绪：创建菜单
func trayReady() {
	systray.SetTitle("小双")
	systray.SetTooltip("小双 🐟 桌面陪伴")
	// 图标：Windows 需要 .ico 格式字节（getlantern/systray 明确要求）
	if img := loadImageFile(mainImgPath); img != nil {
		systray.SetIcon(makeICO(img))
	} else {
		systray.SetIcon(makeICO(iconFallbackImage()))
	}

	mShow := systray.AddMenuItem("显示窗口", "把小双叫回来")
	mQuit := systray.AddMenuItem("退出", "退出小双")

	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				globalWinVisible = true
				if trayWindow != nil {
					// fyneDo 确保 UI 线程执行（视频窗口被 Hide 后直接调用可能不生效）
					fyneDo(func() {
						trayWindow.Show()
						trayWindow.RequestFocus()
					})
				}
			case <-mQuit.ClickedCh:
				trayQuit = true
				if trayWindow != nil {
					trayWindow.Close()
				}
				systray.Quit()
				os.Exit(0) // 托盘退出 = 彻底退出（不再被关窗拦截）
			}
		}
	}()
	fmt.Println("[tray] 系统托盘已就绪（右键退出）")
}

// trayExit systray 退出回调
func trayExit() {
	fmt.Println("[tray] 托盘已退出")
}

// resizeIcon 最近邻缩放图片到指定尺寸，返回 RGBA 字节（托盘图标用，小尺寸防 Windows 不显示）
func resizeIcon(img image.Image, size int) []byte {
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			sx := (x * b.Dx()) / size
			sy := (y * b.Dy()) / size
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	return dst.Pix
}

// makeICO 把图片转成 Windows .ico 格式字节（托盘图标用）
// Windows 的 getlantern/systray 只认 .ico 内容，RGBA 像素直接传不显示
func makeICO(img image.Image) []byte {
	const size = 32
	b := img.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			sx := (x * b.Dx()) / size
			sy := (y * b.Dy()) / size
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	// DIB 像素（BGRA，自下而上）
	pix := make([]byte, size*size*4)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			r, g, bl, a := dst.At(x, size-1-y).RGBA()
			i := (y*size + x) * 4
			pix[i] = byte(bl >> 8)
			pix[i+1] = byte(g >> 8)
			pix[i+2] = byte(r >> 8)
			pix[i+3] = byte(a >> 8)
		}
	}
	andMask := make([]byte, size*size/8) // 1bpp AND mask（全 0 = 不透明）
	// BITMAPINFOHEADER（40 字节）
	hdr := make([]byte, 40)
	binary.LittleEndian.PutUint32(hdr[0:], 40)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(size))
	binary.LittleEndian.PutUint32(hdr[8:], uint32(size*2)) // 高度×2（含 AND mask）
	binary.LittleEndian.PutUint16(hdr[12:], 1)             // planes
	binary.LittleEndian.PutUint16(hdr[14:], 32)            // bitcount
	binary.LittleEndian.PutUint32(hdr[20:], uint32(len(pix)+len(andMask)))
	// ICONDIR（6）+ ICONDIRENTRY（16）
	ico := []byte{0, 0, 1, 0, 1, 0}
	entry := []byte{32, 32, 0, 0, 1, 0, 32, 0}
	dataSize := uint32(len(hdr) + len(pix) + len(andMask))
	entry = append(entry, byte(dataSize), byte(dataSize>>8), byte(dataSize>>16), byte(dataSize>>24))
	entry = append(entry, 22, 0, 0, 0) // 数据偏移（6+16=22）
	ico = append(ico, entry...)
	ico = append(ico, hdr...)
	ico = append(ico, pix...)
	ico = append(ico, andMask...)
	return ico
}

// imageToRGBA 把 image.Image 转成 RGBA 字节（systray 图标用）
func imageToRGBA(img image.Image) []byte {
	b := img.Bounds()
	rgba := image.NewRGBA(b)
	draw.Draw(rgba, b, img, b.Min, draw.Src)
	return rgba.Pix
}

// iconFallbackImage 无主图时的默认图标（简单蓝色圆点）
func iconFallbackImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{R: 66, G: 133, B: 244, A: 255})
		}
	}
	return img
}
