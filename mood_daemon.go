package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
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
		videoDir = filepath.Join(exeDir, "resources")
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

// 情绪 → 视频文件映射（idle 用静态主图）
// 主动情绪用"动作明显+无缝循环"动画（i2v动作版正放倒放，16秒）
var emotionFiles = map[string]string{
	"idle":       "final_v3_1280.png",
	"happy":      "happy_seed.mp4",
	"smile":      "smile_seed.mp4",
	"daze":       "daze_seed.mp4",
	"sad":        "sad_seed.mp4",
	"think":      "think_seed.mp4",
	"trance":     "trance_seed.mp4",
	"surprised":  "surprised_seed.mp4",
	"proud":      "proud_seed.mp4",
	"shy":        "shy_seed.mp4",
	"sleepy":     "sleepy_seed.mp4",
	"angry":      "angry_seed.mp4",
	"excited":    "excited_seed.mp4",
	"crying":     "crying_seed.mp4",
	"wink":       "wink_seed.mp4",
	"wronged":    "wronged_seed.mp4",
	"cute":       "cute_seed.mp4",
	"scared":     "scared_seed.mp4",
	"speechless": "speechless_seed.mp4",
}

// 空闲行为已禁用（用户要求只做表情）
var idleEmotions = []string{}

// 空闲表情动画（主图→表情→主图，循环）
var idleAnims = map[string]string{
	"daze":   "daze_loop.mp4",
	"trance": "trance_loop.mp4",
	"think":  "think_loop.mp4",
	"sleepy": "sleepy_loop.mp4",
	"lie":    "lie_loop.mp4",
	"swing":  "swing_loop.mp4",
}

// 每个行为的动画时长（荡秋千荡久一点）
var animDurs = map[string]time.Duration{
	"daze":   8 * time.Second,
	"trance": 8 * time.Second,
	"think":  8 * time.Second,
	"sleepy": 8 * time.Second,
	"lie":    8 * time.Second,
	"swing":  19 * time.Second,
}

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
	flag.StringVar(&videoDir, "video-dir", videoDir, "素材目录（含主图和动画）")
	flag.StringVar(&moodFile, "mood-file", moodFile, "情绪文件路径（JSON: {\"emotion\":\"happy\"}）")
	flag.Parse()

	fmt.Println("🐟 桌面形象守护进程启动")
	fmt.Printf("  素材目录: %s\n", videoDir)
	fmt.Printf("  情绪文件: %s\n", moodFile)

	// 守护逻辑（mpv + 表情轮询）放 goroutine，主线程跑 Fyne 侧边栏
	go daemonLoop()

	// 侧边栏（Fyne 需要主线程）
	fmt.Println("  💬 侧边栏已启动")
	runSidebar(exeDirOf())
}

func daemonLoop() {
	xauth := getXauthority()
	fmt.Printf("  XAUTHORITY: %s\n", xauth)

	// 启动 mpv，初始显示静态主图
	mpvCmd := startMpv(xauth, filepath.Join(videoDir, "final_v3_1280.png"))
	defer func() {
		if mpvCmd.Process != nil {
			mpvCmd.Process.Kill()
		}
	}()

	conn := waitForIPC()
	defer conn.Close()
	fmt.Println("✅ IPC 已连接")

	// 空闲状态机：displaying(显示主图) / animating(播放表情动画后回主图)
	lastEmotion := "idle"
	idleState := "displaying"
	// 行为浮现间隔：30-60秒随机
	idleInterval := func() time.Duration {
		return time.Duration(30 + rand.Intn(31)) * time.Second
	}
	// 洗牌队列：随机顺序但保证所有行为都出现
	idleQueue := rand.Perm(len(idleEmotions))
	idleQueueIdx := 0
	nextSwitchAt := time.Now().Add(idleInterval())

	for {
		emotion := readMood()

		// 主动情绪优先
		if emotion != "idle" {
			if emotion != lastEmotion {
				f := emotionFiles[emotion]
				if f == "" {
					f = emotionFiles["idle"]
				}
				full := filepath.Join(videoDir, f)
				fmt.Printf("🎭 情绪切换: %s → %s (%s)\n", lastEmotion, emotion, f)
				switchVideo(conn, full)
				lastEmotion = emotion
			}
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// 从主动情绪回到空闲：显示主图
		if lastEmotion != "idle" {
			fmt.Println("🎭 回到空闲, 显示主图")
			switchVideo(conn, filepath.Join(videoDir, "final_v3_1280.png"))
			lastEmotion = "idle"
			idleState = "displaying"
			nextSwitchAt = time.Now().Add(idleInterval())
			time.Sleep(500 * time.Millisecond)
			continue
		}

		// 空闲状态机
		switch idleState {
		case "displaying":
			// 空闲行为已禁用：只显示主图
			if len(idleEmotions) > 0 && time.Now().After(nextSwitchAt) {
				f := idleEmotions[idleQueue[idleQueueIdx]]
				idleQueueIdx++
				if idleQueueIdx >= len(idleQueue) {
					idleQueue = rand.Perm(len(idleEmotions)) // 一轮播完重新洗牌
					idleQueueIdx = 0
				}
				full := filepath.Join(videoDir, idleAnims[f])
				fmt.Printf("🎭 空闲浮现行为: %s\n", f)
				switchVideo(conn, full)
				idleState = "animating"
				nextSwitchAt = time.Now().Add(animDurs[f])
			}
		case "animating":
			// 动画播完回到主图
			if time.Now().After(nextSwitchAt) {
				fmt.Println("🎭 回到主图")
				switchVideo(conn, filepath.Join(videoDir, "final_v3_1280.png"))
				idleState = "displaying"
				nextSwitchAt = time.Now().Add(idleInterval())
			}
		}

		time.Sleep(500 * time.Millisecond)
	}
}
