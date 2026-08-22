package main

import (
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
)

// VideoPlayer ffmpeg 抽帧播放器（无 mpv 依赖，Fyne 内显示视频）
type VideoPlayer struct {
	img     *canvas.Image
	cmd     *exec.Cmd
	stdout  io.ReadCloser
	mu      sync.Mutex
	stopped bool
	onFrame func()
	once    bool   // 单次播放模式（播完回调 onDone，不循环）
	onDone  func() // 单次播放结束回调（goroutine 内调用，内部需自行 fyne.Do）
}

// NewVideoPlayer 创建播放器（img 是显示目标）
func NewVideoPlayer(img *canvas.Image) *VideoPlayer {
	return &VideoPlayer{img: img}
}

// hwaccelArgs 按平台返回硬件解码参数（无硬解时回退软解）
func hwaccelArgs() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"-hwaccel", "d3d11va", "-hwaccel_output_format", "nv12"}
	case "darwin":
		return []string{"-hwaccel", "videotoolbox"}
	default:
		// Linux：优先 AMD VAAPI（检测 render 设备），否则软解
		if dev := findAMDDevice(); dev != "" {
			return []string{"-hwaccel", "vaapi", "-hwaccel_device", dev, "-hwaccel_output_format", "nv12"}
		}
		return nil // 无硬解设备 → 软解
	}
}

// findAMDDevice 扫描 /sys/class/drm 找 AMD GPU 的 render 设备
func findAMDDevice() string {
	entries, err := filepath.Glob("/sys/class/drm/renderD*")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		vendor, err := os.ReadFile(filepath.Join(e, "device", "vendor"))
		if err == nil && strings.TrimSpace(string(vendor)) == "0x1002" {
			return "/dev/dri/" + filepath.Base(e)
		}
	}
	return ""
}

// hwaccelEnv 硬解所需环境变量（Linux VAAPI 指定 AMD 驱动）
func hwaccelEnv() []string {
	if runtime.GOOS != "linux" {
		return nil
	}
	if findAMDDevice() == "" {
		return nil
	}
	return []string{"LIBVA_DRIVER_NAME=radeonsi"}
}

// ffmpegBinPath 返回 ffmpeg 可执行路径（Windows 用 exe 同目录 ffmpeg.exe，其余用系统 ffmpeg）
func ffmpegBinPath() string {
	if isWindows() {
		return filepath.Join(exeDir, "ffmpeg.exe")
	}
	return "ffmpeg"
}

// Play 播放视频文件（循环），fps 抽帧率（默认2）
func (vp *VideoPlayer) Play(videoPath string, fps int) {
	vp.play(videoPath, fps, false, nil)
}

// PlayOnce 播放视频一遍，播完回调 onDone（goroutine 内调用，需自行 fyne.Do 包 UI 更新）
func (vp *VideoPlayer) PlayOnce(videoPath string, fps int, onDone func()) {
	vp.play(videoPath, fps, true, onDone)
}

func (vp *VideoPlayer) play(videoPath string, fps int, once bool, onDone func()) {
	vp.Stop()
	if fps <= 0 {
		fps = 2
	}
	vp.mu.Lock()
	vp.stopped = false
	vp.once = once
	vp.onDone = onDone
	vp.mu.Unlock()
	fmt.Printf("[player] 播放: %s fps=%d once=%v\n", videoPath, fps, once)

	ffArgs := append(hwaccelArgs(), "-i", videoPath,
		"-vf", "fps="+itoa(fps)+",scale=480:270,format=rgba",
		"-f", "rawvideo", "-pix_fmt", "rgba", "-")
	cmd := exec.Command(ffmpegBinPath(), ffArgs...)
	hideWindow(cmd) // Windows 不弹控制台窗口
	fmt.Println("[player] ffmpeg 准备启动(raw管道+硬解)")

	go func() {
		frames := 0
		lastLog := time.Now()
		lastFrames := 0
		var lastFrameAt time.Time
		frameInterval := time.Second / time.Duration(fps)
		// raw 帧大小：480x270x3
		const fw, fh = 480, 270
		frameSize := fw * fh * 4
		// 双缓冲：bufA 显示，bufB 填充
		bufA := make([]byte, frameSize)
		bufB := make([]byte, frameSize)
		cur := bufA
		curIdx := 0
		fill := 0
		for {
			vp.mu.Lock()
			if vp.stopped {
				vp.mu.Unlock()
				return
			}
			vp.mu.Unlock()

			// 帧率统计（用实际经过时间）
			if time.Since(lastLog) > 5*time.Second {
				elapsed := time.Since(lastLog).Seconds()
				rate := float64(frames-lastFrames) / elapsed
				fmt.Printf("[player] 实际帧率: %.1f fps (累计 %d)\n", rate, frames)
				lastLog = time.Now()
				lastFrames = frames
			}

			stdout, err := cmd.StdoutPipe()
			if err != nil {
				fmt.Println("[player] StdoutPipe 失败:", err)
				return
			}
			vp.cmd = cmd
			cmd.Stderr = os.Stderr
			cmd.Env = append(os.Environ(), hwaccelEnv()...)
			if err := cmd.Start(); err != nil {
				fmt.Println("[player] ffmpeg 启动失败:", err)
				return
			}

			buf := make([]byte, 64*1024)
			for {
				vp.mu.Lock()
				if vp.stopped {
					vp.mu.Unlock()
					return
				}
				vp.mu.Unlock()
				n, err := stdout.Read(buf)
				if n > 0 {
					// 填充（支持跨帧数据）
					off := 0
					for off < n {
						remaining := frameSize - fill
						take := n - off
						if take > remaining {
							take = remaining
						}
						copy(cur[fill:], buf[off:off+take])
						fill += take
						off += take
						if fill < frameSize {
							continue
						}
						// 完整帧 → 显示
						frames++
						if frames <= 3 || frames%60 == 0 {
							fmt.Printf("[player] 帧 %d\n", frames)
						}
						// 精确帧率节流
						now := time.Now()
						if !lastFrameAt.IsZero() {
							wait := frameInterval - now.Sub(lastFrameAt)
							if wait > 0 {
								time.Sleep(wait)
								now = time.Now()
							}
						}
						lastFrameAt = now
						img := &image.NRGBA{Pix: cur, Stride: fw * 4, Rect: image.Rect(0, 0, fw, fh)}
						vp.showFrame(img)
						// 切换缓冲
						if curIdx == 0 {
							curIdx = 1
							cur = bufB
						} else {
							curIdx = 0
							cur = bufA
						}
						fill = 0
					}
				}
				if err != nil {
					// 播放结束（EOF）
					fill = 0
					cur = bufA
					curIdx = 0
					if cmd.Process != nil {
						cmd.Process.Kill()
					}
					cmd.Wait()
					// 单次模式：播完回调后退出，不循环
					vp.mu.Lock()
					once := vp.once
					done := vp.onDone
					vp.mu.Unlock()
					if once {
						fmt.Println("[player] 单次播放结束")
						if done != nil {
							done()
						}
						return
					}
					ffArgs := append(hwaccelArgs(), "-i", videoPath,
						"-vf", "fps="+itoa(fps)+",scale=480:270,format=rgba",
						"-f", "rawvideo", "-pix_fmt", "rgba", "-")
					cmd = exec.Command(ffmpegBinPath(), ffArgs...)
					hideWindow(cmd) // Windows 不弹控制台窗口
					break
				}
			}
		}
	}()
}

var (
	showCallCount int
	doExecCount   int
	doExecLastImg string
)

func (vp *VideoPlayer) showFrame(img image.Image) {
	// 拷贝一份（Fyne 异步渲染时不能共享双缓冲，否则被新帧覆盖导致越界）
	if rgba, ok := img.(*image.NRGBA); ok {
		cp := make([]byte, len(rgba.Pix))
		copy(cp, rgba.Pix)
		img = &image.NRGBA{Pix: cp, Stride: rgba.Stride, Rect: rgba.Rect}
	}
	showCallCount++
	fyne.Do(func() {
		doExecCount++
		doExecLastImg = fmt.Sprintf("%dx%d", img.Bounds().Dx(), img.Bounds().Dy())
		vp.img.Image = img
		vp.img.Refresh()
	})
	if showCallCount <= 5 || showCallCount%60 == 0 {
		fmt.Printf("[player] showFrame#%d doExec#%d img=%s\n", showCallCount, doExecCount, doExecLastImg)
	}
}

// Stop 停止播放
func (vp *VideoPlayer) Stop() {
	vp.mu.Lock()
	vp.stopped = true
	cmd := vp.cmd
	vp.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Kill()
		cmd.Wait()
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
