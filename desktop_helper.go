package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ===== Help 桌面观察助手 =====
// 激活后按设置间隔（默认5秒）截图 + PaddleOCR 识别桌面文字：
//   1. 与上次 OCR 结果对比，没变化 → 跳过分析（省 token）
//   2. 有变化 → AI 判断：用户在做什么 / 是否需要帮助 / 心情
//   3. 需要帮助 → 气泡弹出文字帮助；心情好/差 → 播放对应表情
// 主打陪伴感和有用的帮助（间隔可设，帮助/表情有最小间隔防打扰）

var (
	helperMu     sync.Mutex
	helperActive bool
	helperStop   chan struct{}
	lastOCRText  string
	lastHelpAt   time.Time
	lastMoodAt   time.Time
)

const (
	helperMinHelpGap = 5 * time.Minute // 帮助提示最小间隔（防刷屏）
	helperMinMoodGap = 3 * time.Minute // 表情播放最小间隔
	helperDefaultInt = 5               // 默认间隔秒
)

// ocrHelperPath OCR 脚本绝对路径
func ocrHelperPath() string {
	return filepath.Join(exeDirOf(), "scripts", "ocr_helper.py")
}

// venvPython Hermes venv 的 python（paddleocr 装在里面）
func venvPython() string {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, ".hermes", "hermes-agent", "venv", "bin", "python3")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return "python3"
}

// desktopShot 截图全屏（gridhand，继承主机 X 环境；Wayland 下走 Portal）
func desktopShot(path string) error {
	cmd := exec.Command("gridhand", "screenshot", "--output", path)
	cmd.Env = append(os.Environ(), "DISPLAY=:0", "XAUTHORITY="+getXauthority())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("截图失败: %v %s", err, strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("截图文件未生成")
	}
	return nil
}

// ocrDesktop OCR 识别图片文字
func ocrDesktop(imgPath string) (string, error) {
	out, err := runCmd(venvPython(), ocrHelperPath(), imgPath)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// analyzeScreen AI 分析屏幕文字 → (活动, 需要帮助, 帮助文本, 心情)
func analyzeScreen(text string) (string, bool, string, string) {
	prompt := fmt.Sprintf(`屏幕上的文字（OCR结果）：
%s

判断：1) 用户在做什么（写邮件/聊天/编程/看文档/浏览网页等）2) 是否需要帮助（遇到困难、在问问题、有明显可帮忙的地方）3) 用户心情。
只输出JSON：{"activity":"简短活动描述","need_help":true或false,"help_text":"需要帮助时的简短中文建议，20字内，不需要则空字符串","mood":"happy或sad或neutral"}`, text)
	msgs := []ChatMessage{
		{Role: "system", Content: "你是桌面观察助手，只输出JSON。"},
		{Role: "user", Content: prompt},
	}
	reply, err := chatWithAIEx(msgs, false, false)
	if err != nil {
		return "", false, "", "neutral"
	}
	var r struct {
		Activity string `json:"activity"`
		NeedHelp bool   `json:"need_help"`
		HelpText string `json:"help_text"`
		Mood     string `json:"mood"`
	}
	if json.Unmarshal([]byte(reply), &r) != nil {
		return "", false, "", "neutral"
	}
	return r.Activity, r.NeedHelp, r.HelpText, r.Mood
}

// toggleHelper 开关 Help 观察（true=开启，false=停止）
func toggleHelper(on bool) {
	helperMu.Lock()
	defer helperMu.Unlock()
	if on == helperActive {
		return
	}
	helperActive = on
	if on {
		helperStop = make(chan struct{})
		go helperLoop(helperStop)
		fmt.Println("[helper] 桌面观察已启动")
	} else if helperStop != nil {
		close(helperStop)
		fmt.Println("[helper] 桌面观察已停止")
	}
}

// isHelperActive 当前是否在观察
func isHelperActive() bool {
	helperMu.Lock()
	defer helperMu.Unlock()
	return helperActive
}

// helperLoop 观察循环（单 goroutine，间隔可配）
func helperLoop(stop chan struct{}) {
	settingsMu.RLock()
	iv := settings.HelpInterval
	settingsMu.RUnlock()
	interval := time.Duration(iv) * time.Second
	if interval < 3*time.Second {
		interval = helperDefaultInt * time.Second
	}
	for {
		select {
		case <-stop:
			return
		case <-time.After(interval):
		}
		if isTaskBusy() || globalPlayer == nil {
			continue // 忙/未就绪跳过
		}
		helperTick()
	}
}

// helperTick 单轮观察：截图 → OCR → 变化检测 → AI → 行动
func helperTick() {
	img := filepath.Join(os.TempDir(), fmt.Sprintf("helper_%d.png", time.Now().UnixNano()))
	defer os.Remove(img)
	if err := desktopShot(img); err != nil {
		fmt.Printf("[helper] %v\n", err)
		return
	}
	text, err := ocrDesktop(img)
	if err != nil || text == "" {
		fmt.Printf("[helper] OCR: %v\n", err)
		return
	}
	// 变化检测：和上次相同 → 不分析
	helperMu.Lock()
	same := text == lastOCRText
	lastOCRText = text
	helperMu.Unlock()
	if same {
		return
	}
	// AI 分析
	activity, needHelp, helpText, mood := analyzeScreen(text)
	fmt.Printf("[helper] 屏幕变化: %s | 活动=%s 帮助=%v 心情=%s\n",
		truncateRune(text, 40), activity, needHelp, mood)
	// 帮助提示（限频）
	if needHelp && strings.TrimSpace(helpText) != "" && time.Since(lastHelpAt) > helperMinHelpGap {
		lastHelpAt = time.Now()
		msg := "👀 看到你在" + activity + "，" + strings.TrimSpace(helpText)
		globalAddMsg("assistant", msg)
	}
	// 心情表情（限频）
	if mood == "happy" || mood == "sad" {
		if time.Since(lastMoodAt) > helperMinMoodGap {
			lastMoodAt = time.Now()
			if mood == "happy" {
				playExpr("开心", globalPlayer)
			} else {
				playExpr("伤心", globalPlayer)
			}
		}
	}
}
