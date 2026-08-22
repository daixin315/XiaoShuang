package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	videoDir string
	moodFile string
	sockPath string
	mpvBin   string
	mpvArgs  []string
	exeDir   string
)

// exeDirOf 返回可执行文件所在目录
func exeDirOf() string {
	if exeDir != "" {
		return exeDir
	}
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func init() {
	exeDir = exeDirOf()
	if runtime.GOOS == "windows" {
		// Windows 绿色版：所有文件在 exe 同目录，双击即用
		mpvBin = filepath.Join(exeDir, "mpv.exe")
		videoDir = filepath.Join(exeDir, "assets") // 与 Linux 统一目录名
		moodFile = filepath.Join(exeDir, "mood.json")
		sockPath = "127.0.0.1:8712" // Windows 用 TCP IPC
		mpvArgs = []string{
			"--vo=gpu",
			"--no-border",
			"--ontop",
			"--loop-file=inf",
			"--image-display-duration=inf",
			"--input-ipc-server=" + sockPath,
			"--geometry=960x540-0-0", // 右下角 16:9（负数坐标=从右/底边算起）
		}
	} else {
		// Linux：默认 exe 同目录 assets/（可用 flag 覆盖）
		mpvBin = "mpv"
		videoDir = filepath.Join(exeDir, "assets")
		moodFile = filepath.Join(exeDir, "mood.json")
		sockPath = "/tmp/mpv_mood.sock"
		mpvArgs = []string{
			"--vo=xv",
			"--no-border",
			"--ontop",
			"--loop-file=inf",
			"--image-display-duration=inf",
			"--input-ipc-server=" + sockPath,
			"--geometry=640x360+640+360",
		}
	}
}

// 情绪 → 视频文件映射（由 scanResources 动态索引，见 resources.go）
// 旧英文 seed 文件名映射已废弃：assets/表情/ 下是中文文件名

// 空闲行为已禁用（用户要求只做表情）
var idleEmotions = []string{}

// getXauthority 从 Xwayland 进程参数里动态获取当前 auth 路径（带重试，开机时 Xwayland 可能未就绪）
func getXauthority() string {
	if runtime.GOOS == "windows" {
		return "" // Windows 不需要 X11 认证
	}
	for i := 0; i < 60; i++ {
		out, err := exec.Command("bash", "-c",
			"ps aux | grep -i xwayland | grep -v grep | grep -oP 'auth \\K\\S+' | head -1").Output()
		if err == nil {
			p := strings.TrimSpace(string(out))
			if p != "" {
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	return "/run/user/1000/.mutter-Xwaylandauth.KCHDU3"
}

// startMpv 启动 mpv（右下角，无边框置顶）
func startMpv(xauth, video string) *exec.Cmd {
	args := append(append([]string{}, mpvArgs...), video)
	cmd := exec.Command(mpvBin, args...)
	if runtime.GOOS != "windows" {
		cmd.Env = append(os.Environ(),
			"DISPLAY=:0",
			"XAUTHORITY="+xauth,
		)
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		fmt.Printf("❌ mpv 启动失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✅ mpv 已启动 (pid %d)\n", cmd.Process.Pid)
	return cmd
}

// waitForIPC 等待 mpv 的 IPC 就绪并连接（Linux unix socket / Windows TCP）
func waitForIPC() net.Conn {
	for i := 0; i < 50; i++ {
		if runtime.GOOS == "windows" {
			conn, err := net.Dial("tcp", sockPath)
			if err == nil {
				return conn
			}
		} else {
			if _, err := os.Stat(sockPath); err == nil {
				conn, err := net.Dial("unix", sockPath)
				if err == nil {
					return conn
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Println("❌ IPC 连接失败")
	os.Exit(1)
	return nil
}

// switchVideo 通过 IPC 切换视频
func switchVideo(conn net.Conn, path string) {
	cmd := fmt.Sprintf(`{"command": ["loadfile", %q, "replace"]}`, path)
	conn.Write([]byte(cmd + "\n"))
}

// readMood 读情绪状态文件，返回情绪名（默认 idle）
func readMood() string {
	b, err := os.ReadFile(moodFile)
	if err != nil {
		return "idle"
	}
	var m struct {
		Emotion string `json:"emotion"`
	}
	if json.Unmarshal(b, &m) != nil {
		return "idle"
	}
	if m.Emotion == "" {
		return "idle"
	}
	return m.Emotion
}

func main() {
	setupLogFile() // Windows：日志写 exe 同目录 log.txt
	flag.StringVar(&videoDir, "video-dir", videoDir, "素材目录（含主图和动画）")
	flag.StringVar(&moodFile, "mood-file", moodFile, "情绪文件路径（JSON: {\"emotion\":\"happy\"}）")
	noMpv := flag.Bool("no-mpv", true, "一体化模式：不使用独立 mpv 窗口（视频由 Fyne 窗口内播放）")
	flag.Parse()

	fmt.Println("🐟 桌面形象守护进程启动")
	fmt.Printf("  素材目录: %s\n", videoDir)
	fmt.Printf("  情绪文件: %s\n", moodFile)
	scanResources()

	if !*noMpv {
		// 传统模式：独立 mpv 窗口 + 表情轮询（无侧边栏一体化）
		go daemonLoop()
	} else {
		// 一体化模式：只监听 mood.json（外部 set_mood.sh 联动窗口内播放）
		go watchMoodFile()
	}

	// Fyne 一体化窗口（Fyne 需要主线程）
	fmt.Println("  💬 一体化窗口已启动")
	runMainWindow(exeDirOf())
}

// watchMoodFile 监听 mood.json 变化，切换窗口内视频（一体化模式）
func watchMoodFile() {
	last := ""
	for {
		data, err := os.ReadFile(moodFile)
		if err == nil {
			var m struct {
				Emotion string `json:"emotion"`
			}
			if json.Unmarshal(data, &m) == nil && m.Emotion != "" && m.Emotion != last {
				fmt.Printf("  🎭 情绪: %s\n", m.Emotion)
				if globalPlayer != nil {
					vp := resolveEmotion(m.Emotion)
					if vp != "" && fileExists(vp) {
						if vp == mainImgPath {
							// 主图（idle）→ 静态显示
							playMainStatic(globalPlayer)
						} else {
							globalPlayer.Play(vp, 24)
						}
						last = m.Emotion // 只有真正播放了才记录，避免 player 未就绪时漏触发
					}
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
}

func daemonLoop() {
	xauth := getXauthority()
	fmt.Printf("  XAUTHORITY: %s\n", xauth)

	// 启动 mpv，初始显示静态主图
	mpvCmd := startMpv(xauth, mainImgPath)
	defer func() {
		if mpvCmd.Process != nil {
			mpvCmd.Process.Kill()
		}
	}()

	conn := waitForIPC()
	defer conn.Close()
	fmt.Println("✅ IPC 已连接")

	lastEmotion := "idle"

	for {
		emotion := readMood()

		// 主动情绪优先
		if emotion != "idle" {
			if emotion != lastEmotion {
				full := resolveEmotion(emotion)
				if full == "" {
					full = mainImgPath
				}
				fmt.Printf("🎭 情绪切换: %s → %s (%s)\n", lastEmotion, emotion, full)
				switchVideo(conn, full)
				lastEmotion = emotion
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// 从主动情绪回到空闲：显示主图
		if lastEmotion != "idle" {
			fmt.Println("🎭 回到空闲, 显示主图")
			switchVideo(conn, mainImgPath)
			lastEmotion = "idle"
			time.Sleep(500 * time.Millisecond)
			continue
		}

		time.Sleep(500 * time.Millisecond)
	}
}
