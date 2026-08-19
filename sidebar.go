package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// ChatLog 对话记录（内存）
var (
	chatHistory []ChatMessage // 发送给 AI 的历史
	chatLogMu   sync.Mutex
)

// runSidebar 启动侧边栏窗口（阻塞）
func runSidebar(exeDir string) {
	loadSettings(exeDir)

	a := app.NewWithID("fish.desktop.avatar")
	w := a.NewWindow("小双 💬")
	w.Resize(fyne.NewSize(320, 560))

	// 对话记录区（RichText 显示用户/助手消息）
	chatBox := container.NewVBox()
	chatScroll := container.NewVScroll(chatBox)

	addMsg := func(role, text string) {
		fyne.Do(func() {
			prefix := "🧑 你"
			if role == "assistant" {
				prefix = "🐟 小双"
			}
			chatBox.Add(widget.NewLabel(fmt.Sprintf("%s:\n%s\n", prefix, text)))
			chatScroll.ScrollToBottom()
		})
	}

	// 输入行
	input := widget.NewEntry()
	input.SetPlaceHolder("说点什么…")
	sendBtn := widget.NewButton("发送", func() {
		text := strings.TrimSpace(input.Text)
		if text == "" {
			return
		}
		input.SetText("")
		sendChat(text, addMsg)
	})
	input.OnSubmitted = func(_ string) { sendBtn.OnTapped() }

	// 语音按钮（按住说话）
	recBtn := newHoldButton("🎤 按住说话", func() {
		startRecord()
	}, func() {
		stopRecordAndSend(addMsg)
	})

	// 设置按钮
	setBtn := widget.NewButton("⚙️ 设置", func() {
		openSettingsDialog(w)
	})

	// 底部布局
	btns := container.NewHBox(recBtn, setBtn)
	compose := container.NewBorder(nil, nil, nil, sendBtn, input)

	// 根布局
	root := container.NewBorder(
		nil, // top
		container.NewVBox(compose, btns), // bottom
		nil, nil,
		chatScroll,
	)
	w.SetContent(root)
	w.ShowAndRun()
}

// sendChat 发送文字对话
func sendChat(text string, addMsg func(string, string)) {
	chatLogMu.Lock()
	chatHistory = append(chatHistory, ChatMessage{Role: "user", Content: text})
	chatLogMu.Unlock()
	addMsg("user", text)
	setMoodNow("think") // 思考表情

	go func() {
		chatLogMu.Lock()
		h := append([]ChatMessage{}, chatHistory...)
		chatLogMu.Unlock()
		reply, err := chatWithAI(h)
		if err != nil {
			reply = "⚠️ " + err.Error()
		}
		addMsg("assistant", reply)
		chatLogMu.Lock()
		chatHistory = append(chatHistory, ChatMessage{Role: "assistant", Content: reply})
		chatLogMu.Unlock()
		if !strings.HasPrefix(reply, "⚠️") {
			setMoodNow("happy") // 完成表情
		} else {
			setMoodNow("sad")
		}
	}()
}

// setMoodNow 写 mood.json（联动桌面形象）
func setMoodNow(emotion string) {
	settingsMu.RLock()
	mf := moodFile
	settingsMu.RUnlock()
	os.WriteFile(mf, []byte(fmt.Sprintf(`{"emotion":%q}`, emotion)), 0o644)
}

// ---------- 录音 ----------
var (
	recCmd  *exec.Cmd
	recFile string
	recMu   sync.Mutex
)

func startRecord() {
	recMu.Lock()
	defer recMu.Unlock()
	if recCmd != nil {
		return
	}
	recFile = filepath.Join(os.TempDir(), fmt.Sprintf("voice_%d.wav", time.Now().UnixNano()))
	settingsMu.RLock()
	dev := settings.RecordDev
	settingsMu.RUnlock()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		d := dev
		if d == "" {
			d = "麦克风"
		}
		cmd = exec.Command("ffmpeg", "-y", "-f", "dshow", "-i", "audio="+d, recFile)
	} else if runtime.GOOS == "darwin" {
		cmd = exec.Command("ffmpeg", "-y", "-f", "avfoundation", "-i", ":0", recFile)
	} else {
		cmd = exec.Command("ffmpeg", "-y", "-f", "pulse", "-i", "default", recFile)
	}
	if err := cmd.Start(); err == nil {
		recCmd = cmd
	}
}

func stopRecordAndSend(addMsg func(string, string)) {
	recMu.Lock()
	cmd := recCmd
	recCmd = nil
	f := recFile
	recMu.Unlock()
	if cmd == nil {
		return
	}
	cmd.Process.Signal(os.Interrupt)
	go func() {
		cmd.Wait()
		// 如果中断没生成完整文件，等文件存在
		for i := 0; i < 10 && !fileExists(f); i++ {
			time.Sleep(200 * time.Millisecond)
		}
		if !fileExists(f) {
			addMsg("assistant", "⚠️ 录音失败，请检查麦克风")
			return
		}
		// STT 识别
		addMsg("user", "🎤 (语音…)")
		text, err := sttLocal(f)
		os.Remove(f)
		if err != nil || strings.TrimSpace(text) == "" {
			addMsg("assistant", "⚠️ 没听清，再说一次？")
			return
		}
		addMsg("user", text)
		chatLogMu.Lock()
		chatHistory = append(chatHistory, ChatMessage{Role: "user", Content: text})
		chatLogMu.Unlock()

		// AI 回复 + TTS
		setMoodNow("think")
		chatLogMu.Lock()
		h := append([]ChatMessage{}, chatHistory...)
		chatLogMu.Unlock()
		reply, err := chatWithAI(h)
		if err != nil {
			reply = "⚠️ " + err.Error()
		}
		addMsg("assistant", reply)
		chatLogMu.Lock()
		chatHistory = append(chatHistory, ChatMessage{Role: "assistant", Content: reply})
		chatLogMu.Unlock()
		setMoodNow("happy")

		// TTS 播放回复
		if !strings.HasPrefix(reply, "⚠️") {
			mp3 := strings.TrimSuffix(f, ".wav") + ".mp3"
			if err := ttsEdge(reply, mp3); err == nil {
				playAudio(mp3)
			}
		}
	}()
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// playAudio 用 mpv 播放音频（后台）
func playAudio(path string) {
	go func() {
		cmd := exec.Command("mpv", "--no-video", path)
		cmd.Start()
		cmd.Wait()
		os.Remove(path)
	}()
}

// ---------- 设置窗口 ----------
func openSettingsDialog(parent fyne.Window) {
	settingsMu.RLock()
	s := settings
	settingsMu.RUnlock()

	base := widget.NewEntry()
	base.SetText(s.BaseURL)
	key := widget.NewPasswordEntry()
	key.SetText(s.APIKey)
	model := widget.NewEntry()
	model.SetText(s.Model)
	system := widget.NewMultiLineEntry()
	system.SetText(s.System)
	sttModel := widget.NewSelectEntry([]string{"tiny", "base", "small", "medium", "large-v3"})
	sttModel.SetText(s.STTModel)

	form := dialog.NewForm("⚙️ 设置", "保存", "取消",
		[]*widget.FormItem{
			widget.NewFormItem("API 地址", base),
			widget.NewFormItem("API Key", key),
			widget.NewFormItem("模型", model),
			widget.NewFormItem("人设", system),
			widget.NewFormItem("语音识别模型", sttModel),
		},
		func(ok bool) {
			if !ok {
				return
			}
			settingsMu.Lock()
			settings.BaseURL = strings.TrimSpace(base.Text)
			settings.APIKey = strings.TrimSpace(key.Text)
			settings.Model = strings.TrimSpace(model.Text)
			settings.System = system.Text
			settings.STTModel = sttModel.Text
			settingsMu.Unlock()
			saveSettings()
		}, parent)
	form.Resize(fyne.NewSize(420, 520))
	form.Show()
}

// ---------- 按住按钮 ----------
type holdButton struct {
	widget.BaseWidget
	label    string
	onPress  func()
	onRelase func()
}

func newHoldButton(label string, onPress, onRelease func()) *holdButton {
	b := &holdButton{label: label, onPress: onPress, onRelase: onRelease}
	b.ExtendBaseWidget(b)
	return b
}

func (b *holdButton) CreateRenderer() fyne.WidgetRenderer {
	btn := widget.NewButton(b.label, nil)
	btn.Disable()
	return widget.NewSimpleRenderer(btn)
}

// 用点击模拟（简化：单击开始，再单击停止）
var _ = binding.NewString

// holdState 记录当前是否按住
var holdPressed bool

func (b *holdButton) Tapped(_ *fyne.PointEvent) {
	if !holdPressed {
		holdPressed = true
		if b.onPress != nil {
			b.onPress()
		}
	} else {
		holdPressed = false
		if b.onRelase != nil {
			b.onRelase()
		}
	}
}

// 实现 Pointerable 支持按住（桌面）
func (b *holdButton) MouseDown(_ *desktop.MouseEvent) {
	if !holdPressed {
		holdPressed = true
		if b.onPress != nil {
			b.onPress()
		}
	}
}

func (b *holdButton) MouseUp(_ *desktop.MouseEvent) {
	if holdPressed {
		holdPressed = false
		if b.onRelase != nil {
			b.onRelase()
		}
	}
}
