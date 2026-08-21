package main

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	lastShotHash string // 图片 md5（vision 模式变化检测）
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
	return parseScreenJSON(reply)
}

// visionPrompt 视觉直看的分析提示
const visionPrompt = `这张截图是用户的电脑屏幕。判断：1) 用户在做什么（写邮件/聊天/编程/看文档/浏览网页/看K线图等）2) 是否需要帮助（遇到困难、在问问题、有明显可帮忙的地方）3) 用户心情。
只输出JSON：{"activity":"简短活动描述","need_help":true或false,"help_text":"需要帮助时的简短中文建议，20字内，不需要则空字符串","mood":"happy或sad或neutral"}`

// analyzeScreenVision deepseek 视觉模型直看截图 → (活动, 需要帮助, 帮助文本, 心情)
func analyzeScreenVision(imgPath string) (string, bool, string, string) {
	reply, err := visionAnalyze(imgPath, visionPrompt)
	if err != nil {
		fmt.Printf("[helper] vision: %v\n", err)
		return "", false, "", "neutral"
	}
	return parseScreenJSON(reply)
}

// parseScreenJSON 解析 AI 返回的 JSON
func parseScreenJSON(reply string) (string, bool, string, string) {
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

// visionAnalyze 调用 deepseek 视觉模型分析图片（OpenAI 兼容，图片 base64 内联）
func visionAnalyze(imgPath, prompt string) (string, error) {
	settingsMu.RLock()
	base := settings.BaseURL
	key := settings.APIKey
	settingsMu.RUnlock()
	if base == "" || key == "" {
		return "", fmt.Errorf("api_not_configured")
	}
	data, err := os.ReadFile(imgPath)
	if err != nil {
		return "", err
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	payload := map[string]any{
		"model": "deepseek-v4-flash-vision-exp",
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "image_url", "image_url": map[string]string{"url": "data:image/png;base64," + b64}},
					{"type": "text", "text": prompt},
				},
			},
		},
		"max_tokens": 800,
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", strings.TrimSuffix(base, "/")+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	httpCli := &http.Client{Timeout: 60 * time.Second}
	resp, err := httpCli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		msg := string(b)
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return "", fmt.Errorf("vision HTTP %d: %s", resp.StatusCode, msg)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(b, &out) != nil || len(out.Choices) == 0 {
		return "", fmt.Errorf("vision 响应解析失败")
	}
	return out.Choices[0].Message.Content, nil
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

// helperTick 单轮观察：截图 → 按方案(deepseek vision / paddle OCR) → 变化检测 → AI → 行动
func helperTick() {
	img := filepath.Join(os.TempDir(), fmt.Sprintf("helper_%d.png", time.Now().UnixNano()))
	defer os.Remove(img)
	if err := desktopShot(img); err != nil {
		fmt.Printf("[helper] %v\n", err)
		return
	}
	visionMode := helperVision()
	var activity, helpText, mood string
	var needHelp bool

	if visionMode == "deepseek" {
		// 方案1: deepseek vision 直看截图（能理解布局/图标，不只是文字）
		h := md5.Sum(mustReadFile(img))
		hash := fmt.Sprintf("%x", h)
		helperMu.Lock()
		if hash == lastShotHash {
			helperMu.Unlock()
			return // 画面没变化 → 跳过
		}
		lastShotHash = hash
		helperMu.Unlock()
		activity, needHelp, helpText, mood = analyzeScreenVision(img)
	} else {
		// 方案2: PaddleOCR 识别文字 → AI 分析
		text, err := ocrDesktop(img)
		if err != nil || text == "" {
			fmt.Printf("[helper] OCR: %v\n", err)
			return
		}
		helperMu.Lock()
		same := text == lastOCRText
		lastOCRText = text
		helperMu.Unlock()
		if same {
			return
		}
		activity, needHelp, helpText, mood = analyzeScreen(text)
	}

	fmt.Printf("[helper] 屏幕变化: %s | 活动=%s 帮助=%v 心情=%s\n",
		truncateRune(activity, 20), activity, needHelp, mood)
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

// mustReadFile 读文件（失败返回 nil，md5 对空算也无妨）
func mustReadFile(path string) []byte {
	b, _ := os.ReadFile(path)
	return b
}
