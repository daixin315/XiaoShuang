package main

import (
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
	// 图标：用小双主图（main.png），失败则用默认
	if img := loadImageFile(mainImgPath); img != nil {
		// 转成 RGBA 位图给 systray
		systray.SetIcon(imageToRGBA(img))
	} else {
		systray.SetIcon(iconFallback())
	}

	mShow := systray.AddMenuItem("显示窗口", "把小双叫回来")
	mQuit := systray.AddMenuItem("退出", "退出小双")

	go func() {
		for {
			select {
			case <-mShow.ClickedCh:
				globalWinVisible = true
				if trayWindow != nil {
					trayWindow.Show()
					trayWindow.RequestFocus()
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

// imageToRGBA 把 image.Image 转成 RGBA 字节（systray 图标用）
func imageToRGBA(img image.Image) []byte {
	b := img.Bounds()
	rgba := image.NewRGBA(b)
	draw.Draw(rgba, b, img, b.Min, draw.Src)
	return rgba.Pix
}

// iconFallback 无主图时的默认图标（简单蓝色圆点）
func iconFallback() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{R: 66, G: 133, B: 244, A: 255})
		}
	}
	return img.Pix
}
