package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"io"
	"math/bits"
	"net/http"
	"os"
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
	helperMu          sync.Mutex
	helperActive      bool
	helperStop        chan struct{}
	lastOCRText       string
	lastShotGray      []byte    // 上一张 64x64 灰度缩略图（像素差异检测）
	lastActivity      string    // 上次分析的活动
	visionErrNotified bool      // vision 失败是否已提示过（防重复弹）
	lastCompanionAt   time.Time // 变化反馈播报限频
)

const (
	helperDefaultInt = 5    // 默认间隔秒（5秒看一次）
	helperPixThresh  = 0.02 // 像素差异比例阈值（打字/滚动触发，光标/时钟忽略）
)

// dHash 计算图片感知哈希（64 位）：缩 9x8 灰度 → 相邻像素梯度
func dHash(img image.Image) uint64 {
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW == 0 || srcH == 0 {
		return 0
	}
	const gw, gh = 9, 8
	gray := make([]float64, gw*gh)
	for gy := 0; gy < gh; gy++ {
		for gx := 0; gx < gw; gx++ {
			x0, x1 := gx*srcW/gw, (gx+1)*srcW/gw
			y0, y1 := gy*srcH/gh, (gy+1)*srcH/gh
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			var sum float64
			n := 0
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					r, g, bl, _ := img.At(x, y).RGBA()
					sum += 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(bl>>8)
					n++
				}
			}
			gray[gy*gw+gx] = sum / float64(n)
		}
	}
	var h uint64
	for gy := 0; gy < gh; gy++ {
		for gx := 0; gx < 8; gx++ {
			if gray[gy*gw+gx] > gray[gy*gw+gx+1] {
				h |= 1 << uint(gy*8+gx)
			}
		}
	}
	return h
}

// hammingDist 汉明距离（两 hash 不同位数）
func hammingDist(a, b uint64) int {
	return bits.OnesCount64(a ^ b)
}

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

// desktopShot 截图全屏（平台分派：Linux gridhand / Windows GDI）
func desktopShot(path string) error {
	return captureScreen(path)
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

判断：1) 用户在做什么（写邮件/聊天/编程/看文档/浏览网页等）2) 是否需要帮助：
- 遇到困难、在问问题、有明显可帮忙的地方
- 重点1：如果用户在与人聊天（微信/QQ/邮件等），对方使用的语言不是中文——用电脑的人只懂中文 → 需要帮助，且 help_text 直接给出【对方消息的中文翻译】+【用对方语言回复的建议内容】，不用问"要不要翻译"，直接给
- 重点2：如果用户在炒股/看K线/看行情走势 → 需要帮助，help_text 给出简短的风险分析（如大盘/个股走势风险提示）
3) 用户心情。
只输出JSON：{"activity":"简短活动描述","need_help":true或false,"help_text":"需要帮助时的内容（40-100字，如'对方用英文问价格，翻译：价格是多少？建议回复：The price is about 20 dollars per unit.' 或 '大盘MA5向下，个股缩量反弹，注意风险'），不需要则空字符串","mood":"happy或sad或neutral"}`, text)
	if extra := helperExtraPrompt(); extra != "" {
		prompt += "\n额外要求：" + extra
	}
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
const visionPrompt = `这张截图是用户的电脑屏幕。判断：
1) 用户在做什么（写邮件/聊天/编程/看文档/浏览网页/看K线图等）
2) 是否需要帮助：
   - 遇到困难、在问问题、有明显可帮忙的地方
   - 重点1：如果用户在与人聊天（微信/QQ/邮件等），对方使用的语言不是中文——用电脑的人只懂中文 → 需要帮助，且 help_text 直接给出【对方消息的中文翻译】+【用对方语言回复的建议内容】，不用问"要不要翻译"，直接给
   - 重点2：如果用户在炒股/看K线/看行情走势 → 需要帮助，help_text 给出简短的风险分析（如大盘/个股走势风险提示）
3) 用户心情
只输出JSON：{"activity":"简短活动描述","need_help":true或false,"help_text":"需要帮助时的内容（40-100字，如'对方用英文问价格，翻译：价格是多少？建议回复：The price is about 20 dollars per unit.' 或 '大盘MA5向下，个股缩量反弹，注意风险'），不需要则空字符串","mood":"happy或sad或neutral"}`

// analyzeScreenVision deepseek 视觉模型直看截图 → (活动, 需要帮助, 帮助文本, 心情)
// 失败时弹一次可见提示（API 未配置/调用失败），不再无声无息
func analyzeScreenVision(imgPath string) (string, bool, string, string) {
	reply, err := visionAnalyze(imgPath, buildVisionPrompt())
	if err != nil {
		fmt.Printf("[helper] vision 失败: %v\n", err)
		helperMu.Lock()
		notified := visionErrNotified
		visionErrNotified = true
		helperMu.Unlock()
		if !notified && globalAddMsg != nil {
			globalAddMsg("assistant", "🤔 Help 需要 DeepSeek API Key——去 ⚙️ 设置里填一下（或检查网络），我就能看屏幕帮你啦")
		}
		return "", false, "", "neutral"
	}
	helperMu.Lock()
	visionErrNotified = false
	helperMu.Unlock()
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
	httpCli := &http.Client{Timeout: 15 * time.Second} // 15s 超时，失败快速返回不卡轮次
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

// helperLoop 观察循环（单 goroutine；每轮：截图→分析→完成后等 5 秒再下一轮）
func helperLoop(stop chan struct{}) {
	settingsMu.RLock()
	iv := settings.HelpInterval
	settingsMu.RUnlock()
	interval := time.Duration(iv) * time.Second
	if interval < 3*time.Second {
		interval = helperDefaultInt * time.Second
	}
	helperTick() // 触发后立即先跑一轮（不等 5 秒）
	for {
		select {
		case <-stop:
			return
		case <-time.After(interval):
		}
		if isTaskBusy() || globalPlayer == nil {
			continue // 忙/未就绪跳过
		}
		helperTick() // 完成上一轮后才计时等 5 秒（等服务器返回再算间隔）
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
	if isWindows() {
		visionMode = "deepseek" // Windows 无 PaddleOCR 环境，强制 DeepSeek Vision
	}
	var activity, helpText, mood string
	var needHelp bool

	if visionMode == "deepseek" {
		// 方案1: deepseek vision 直看截图（能理解布局/图标，不只是文字）
		// 变化检测：像素差异比例（64x64 灰度缩略图对比）
		//   比例 > 2% → 有实质内容变化（打字/滚动/切页）→ 分析
		//   比例 ≤ 2% → 细小变化（光标/时钟/动画帧）→ 跳过
		_, gray := shotFeatures(img)
		helperMu.Lock()
		ratio := 0.0
		if len(lastShotGray) == len(gray) && len(gray) > 0 {
			ratio = grayDiffRatio(lastShotGray, gray)
		}
		// 第一次（无基准图）或差异超阈值 → 触发分析
		changed := len(lastShotGray) == 0 || ratio > helperPixThresh
		if changed {
			lastShotGray = gray
		}
		helperMu.Unlock()
		if !changed {
			return // 无显著变化 → 跳过
		}
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
	// 有明确帮助建议（含外语沟通翻译帮助）→ 弹提示；否则保持安静
	if needHelp && strings.TrimSpace(helpText) != "" {
		globalAddMsg("assistant", "👀 看到你在"+activity+"，"+strings.TrimSpace(helpText))
	}
	lastActivity = activity
}

// helperOnce 一次性分析：截图当前桌面 → 分析 → 给建议（点"帮助"按钮，只分析一次）
func helperOnce() {
	img := filepath.Join(os.TempDir(), fmt.Sprintf("helper_once_%d.png", time.Now().UnixNano()))
	defer os.Remove(img)
	if err := desktopShot(img); err != nil {
		fmt.Printf("[helper] %v\n", err)
		globalAddMsg("assistant", "😅 截图失败，帮我看看日志…")
		return
	}
	visionMode := helperVision()
	if isWindows() {
		visionMode = "deepseek" // Windows 无 PaddleOCR 环境，强制 DeepSeek Vision
	}
	var activity, helpText string
	var needHelp bool
	if visionMode == "deepseek" {
		activity, needHelp, helpText, _ = analyzeScreenVision(img)
	} else {
		text, err := ocrDesktop(img)
		if err != nil || text == "" {
			fmt.Printf("[helper] OCR: %v\n", err)
			globalAddMsg("assistant", "😅 没识别出屏幕内容，换 DeepSeek Vision 试试？")
			return
		}
		activity, needHelp, helpText, _ = analyzeScreen(text)
	}
	fmt.Printf("[helper] 一次性分析: 活动=%s 帮助=%v\n", activity, needHelp)
	if needHelp && strings.TrimSpace(helpText) != "" {
		globalAddMsg("assistant", "👀 看到你在"+activity+"，"+strings.TrimSpace(helpText))
	} else if activity != "" {
		globalAddMsg("assistant", "👀 看到你在"+activity+"～看起来一切正常，需要帮忙随时说！")
	} else {
		globalAddMsg("assistant", "🤔 没看清你的屏幕在做什么…换个窗口试试？")
	}
}

// shotFeatures 读取截图：返回 dHash（64位）+ 64x64 灰度缩略图（内容变化检测）
func shotFeatures(path string) (uint64, []byte) {
	f, err := os.Open(path)
	if err != nil {
		return 0, nil
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return 0, nil
	}
	return dHash(img), grayThumb(img)
}

// grayThumb 把图片降采样为 64x64 灰度（块平均）
func grayThumb(img image.Image) []byte {
	b := img.Bounds()
	srcW, srcH := b.Dx(), b.Dy()
	if srcW == 0 || srcH == 0 {
		return nil
	}
	const gs = 64
	out := make([]byte, gs*gs)
	for gy := 0; gy < gs; gy++ {
		for gx := 0; gx < gs; gx++ {
			x0, x1 := gx*srcW/gs, (gx+1)*srcW/gs
			y0, y1 := gy*srcH/gs, (gy+1)*srcH/gs
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			var sum uint32
			n := 0
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					r, g, bl, _ := img.At(x, y).RGBA()
					sum += (299*r + 587*g + 114*bl) / 1000 >> 8
					n++
				}
			}
			if n > 0 {
				out[gy*gs+gx] = byte(sum / uint32(n))
			}
		}
	}
	return out
}

// grayDiffRatio 两张缩略图的差异像素比例（差异 > 25 才算变化）
func grayDiffRatio(a, b []byte) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	diff := 0
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		if d > 25 {
			diff++
		}
	}
	return float64(diff) / float64(len(a))
}
