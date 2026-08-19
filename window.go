package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

// 全局视频播放器（daemonLoop 和窗口共用）
var globalPlayer *VideoPlayer

// 全局视频显示对象（playMainStatic 回主图用）
var globalVideoImg *canvas.Image

// 测试句柄（fyne test 驱动 UI 用；生产无影响）
var (
	testInput   *widget.Entry
	testCmdMenu *widget.PopUpMenu
)

// ChatLog 对话记录（内存）
var (
	chatHistory []ChatMessage
	chatLogMu   sync.Mutex
)

// fyneDo fyne.Do 包装（供非 UI 线程安全更新界面）
func fyneDo(f func()) {
	fyne.Do(f)
}

// 测试模式标志（测试文件 TestMain 置 true 后复用 fyne test app）
var useTestApp bool

// runMainWindow 一体化窗口（视频 + 浮动对话 + 输入 + 底部栏）
func runMainWindow(exeDir string) {
	loadSettings(exeDir)
	scanResources()

	var a fyne.App
	if useTestApp {
		a = fyne.CurrentApp() // 测试环境复用 test app（window_test.go 注入）
	} else {
		a = app.NewWithID("fish.desktop.avatar")
	}
	w := a.NewWindow("小双 🐟")
	w.Resize(fyne.NewSize(480, 860))

	// ===== 视频区 =====
	videoImg := canvas.NewImageFromImage(nil)
	videoImg.FillMode = canvas.ImageFillContain
	videoImg.SetMinSize(fyne.NewSize(480, 270))
	globalVideoImg = videoImg
	player := NewVideoPlayer(videoImg)
	globalPlayer = player

	// 初始显示主图
	playMainStatic(player)

	// ===== 对话气泡层（浮动在视频上）=====
	bubbleBox := container.NewVBox()
	bubbleScroll := container.NewVScroll(bubbleBox)
	bubbleScroll.SetMinSize(fyne.NewSize(480, 270))

	addMsg := func(role, text string) {
		fyneDo(func() {
			prefix := "🧑 你"
			if role == "assistant" {
				prefix = "🐟 小双"
			}
			// 半透明气泡
			bg := canvas.NewRectangle(color.NRGBA{R: 20, G: 20, B: 30, A: 200})
			content := container.NewVBox(
				widget.NewLabelWithStyle(prefix, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				widget.NewLabel(text),
			)
			bubble := container.NewStack(bg, container.NewPadded(content))
			bubbleBox.Add(bubble)
			bubbleScroll.ScrollToBottom()
		})
	}

	// ===== 输入行 =====
	input := widget.NewEntry()
	testInput = input
	input.SetPlaceHolder("输入 / 打开表情·动作菜单，或直接聊天…")

	// "/" 命令菜单：输入 / 弹出表情+动作选择，点选/回车执行
	var cmdMenu *widget.PopUpMenu
	showCmdMenu := func() {
		fmt.Println("[menu] showCmdMenu 调用")
		if cmdMenu != nil {
			cmdMenu.Hide()
		}
		exprItems := []*fyne.MenuItem{}
		for _, n := range exprNames {
			n := n
			exprItems = append(exprItems, fyne.NewMenuItem("😊 "+n, func() {
				input.SetText("") // 菜单命令不进入聊天
				playExpr(n, player)
			}))
		}
		actItems := []*fyne.MenuItem{}
		for _, n := range actionNames {
			n := n
			actItems = append(actItems, fyne.NewMenuItem("🎬 "+n, func() {
				input.SetText("") // 菜单命令不进入聊天
				playAction(n, player)
			}))
		}
		exprItem := fyne.NewMenuItem("😊 表情", nil)
		exprItem.ChildMenu = fyne.NewMenu("表情", exprItems...)
		actItem := fyne.NewMenuItem("🎬 动作", nil)
		actItem.ChildMenu = fyne.NewMenu("动作", actItems...)
		// 第 3 项：取消当前播放，回主图
		cancelItem := fyne.NewMenuItem("🏠 回主图", func() {
			input.SetText("")
			playMainStatic(player)
		})
		cmdMenu = widget.NewPopUpMenu(fyne.NewMenu("", exprItem, actItem, fyne.NewMenuItemSeparator(), cancelItem), w.Canvas())
		testCmdMenu = cmdMenu
		cmdMenu.OnDismiss = func() {
			// 关闭时清掉以 / 开头的输入（菜单命令不进入聊天）
			if strings.HasPrefix(strings.TrimSpace(input.Text), "/") {
				input.SetText("")
			}
		}
		// 显示在输入框上方
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(input)
		cmdMenu.ShowAtPosition(pos.Add(fyne.NewPos(0, -30)))
	}
	input.OnChanged = func(text string) {
		if strings.TrimSpace(text) == "/" {
			fmt.Println("[menu] 输入 / 触发菜单")
			showCmdMenu()
		}
	}

	sendBtn := widget.NewButton("发送", func() {
		text := strings.TrimSpace(input.Text)
		if text == "" || text == "/" {
			return
		}
		input.SetText("")
		sendChat(text, addMsg, player)
	})
	input.OnSubmitted = func(_ string) {
		text := strings.TrimSpace(input.Text)
		if text == "/" {
			showCmdMenu() // 输入 / 后直接回车也弹菜单
			return
		}
		sendBtn.OnTapped()
	}
	compose := container.NewBorder(nil, nil, nil, sendBtn, input)

	// ===== 底部栏（表情动作菜单 + 语言 + 设置）=====
	menuBtn := widget.NewButton("🎭 表情/动作", func() { showCmdMenu() })
	langSel := widget.NewSelect([]string{"中文", "English"}, func(v string) {
		settingsMu.Lock()
		if v == "English" {
			settings.System = "You are a cute and gentle girl named Xiaoshuang, a Pisces. Reply briefly and warmly, like a friend."
		} else {
			settings.System = "你是一个温柔可爱的少女，名字叫小双，是双鱼座。回复要简短自然，语气温柔亲切，像朋友一样聊天。"
		}
		settingsMu.Unlock()
	})
	langSel.SetSelected("中文")
	setBtn := widget.NewButton("⚙️ 设置", func() { openSettingsDialog(w) })

	// ===== 语音按钮（按住说话）=====
	recBtn := newHoldButton("🎤 按住说话", startRecord, func() { stopRecordAndSend(addMsg, player) })

	bottomBar := container.NewHBox(menuBtn, recBtn, setBtn, langSel)

	// ===== 根布局：视频(上) + 输入(中) + 底部(下) =====
	bubbleLayer := container.NewBorder(
		nil,       // top
		bubbleBox, // bottom（气泡从底部向上浮动）
		nil, nil, nil,
	)
	videoStack := container.NewStack(videoImg, bubbleLayer)
	root := container.NewBorder(
		nil,
		container.NewVBox(compose, bottomBar),
		nil, nil,
		videoStack,
	)
	w.SetContent(root)
	w.ShowAndRun()
}

// playExpr 播放表情动画（循环，保持情绪），并写入 mood.json
func playExpr(name string, player *VideoPlayer) {
	vp := exprFiles[name]
	if vp == "" {
		return
	}
	player.Play(vp, 24)
	os.WriteFile(moodFile, []byte(fmt.Sprintf(`{"emotion":%q}`, name)), 0o644)
	fmt.Printf("🎭 表情: %s\n", name)
}

// playAction 播放动作动画（单次，播完自动回主图）
func playAction(name string, player *VideoPlayer) {
	vp := actionFiles[name]
	if vp == "" {
		return
	}
	// 写 mood.json 回 idle，避免外部联动停在旧情绪
	os.WriteFile(moodFile, []byte(`{"emotion":"idle"}`), 0o644)
	fmt.Printf("🎬 动作: %s (单次)\n", name)
	player.PlayOnce(vp, 24, func() {
		playMainStatic(player)
	})
}

// loadImageFile 加载图片文件
func loadImageFile(path string) image.Image {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, err := imageDecode(f)
	if err != nil {
		return nil
	}
	return img
}

// sendChat 发送文字对话（播放器联动表情）
func sendChat(text string, addMsg func(string, string), player *VideoPlayer) {
	chatLogMu.Lock()
	chatHistory = append(chatHistory, ChatMessage{Role: "user", Content: text})
	chatLogMu.Unlock()
	addMsg("user", text)
	setMoodNow("think", player)

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
			setMoodNow("happy", player)
		} else {
			setMoodNow("sad", player)
		}
	}()
}

// setMoodNow 写 mood.json + 播放对应表情视频（英文或中文情绪名都支持）
func setMoodNow(emotion string, player *VideoPlayer) {
	os.WriteFile(moodFile, []byte(fmt.Sprintf(`{"emotion":%q}`, emotion)), 0o644)
	if player == nil {
		return
	}
	vp := resolveEmotion(emotion)
	if vp == "" || vp == mainImgPath {
		return
	}
	if fileExists(vp) {
		player.Play(vp, 24)
	}
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
	switch {
	case isWindows():
		d := dev
		if d == "" {
			d = "麦克风"
		}
		cmd = exec.Command("ffmpeg", "-y", "-f", "dshow", "-i", "audio="+d, recFile)
	case isDarwin():
		cmd = exec.Command("ffmpeg", "-y", "-f", "avfoundation", "-i", ":0", recFile)
	default:
		cmd = exec.Command("ffmpeg", "-y", "-f", "pulse", "-i", "default", recFile)
	}
	if err := cmd.Start(); err == nil {
		recCmd = cmd
	}
}

func stopRecordAndSend(addMsg func(string, string), player *VideoPlayer) {
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
		for i := 0; i < 10 && !fileExists(f); i++ {
			time.Sleep(200 * time.Millisecond)
		}
		if !fileExists(f) {
			addMsg("assistant", "⚠️ 录音失败，请检查麦克风")
			return
		}
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

		setMoodNow("think", player)
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
		setMoodNow("happy", player)

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

// playAudio 系统播放音频（后台）
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
