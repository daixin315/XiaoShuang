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

// globalAddMsg 全局对话气泡函数（runMainWindow 赋值；http 接口等外部模块用）
var globalAddMsg func(role, text string)

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

// 静默总结：一轮对话 = 用户停止发言 5 分钟；到点总结新增消息
var (
	summarizeTimer *time.Timer
	lastSummaryIdx int
)

// scheduleSummarize 重置 5 分钟静默计时器（用户每发一条消息调用）
func scheduleSummarize() {
	if summarizeTimer != nil {
		summarizeTimer.Stop()
	}
	summarizeTimer = time.AfterFunc(5*time.Minute, func() {
		chatLogMu.Lock()
		start := lastSummaryIdx
		if start > len(chatHistory) {
			start = 0 // 历史被截断过，从头总结
		}
		newMsgs := append([]ChatMessage{}, chatHistory[start:]...)
		lastSummaryIdx = len(chatHistory)
		chatLogMu.Unlock()
		if len(newMsgs) >= 2 { // 至少一问一答才算一轮
			fmt.Printf("[memory] 对话静默5分钟，总结 %d 条新消息\n", len(newMsgs))
			enqueue(func() { summarizeForMemory(newMsgs) }) // 排队串行，不和主对话抢
		}
	})
}

// fyneDo fyne.Do 包装（供非 UI 线程安全更新界面）
func fyneDo(f func()) {
	fyne.Do(f)
}

// fixedChatLayout 固定中间对话区高度的三栏布局：
//
//	0: 视频（固定顶部） 1: 对话区（固定 chatH） 2: 底部（按自身高度，贴窗口底）
type fixedChatLayout struct{ chatH float32 }

func (l fixedChatLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	if len(objects) < 3 {
		return
	}
	videoH := objects[0].MinSize().Height
	bottomH := objects[2].MinSize().Height
	chatH := l.chatH
	if size.Height < videoH+chatH+bottomH { // 窗口太矮时优先保视频和底部
		chatH = size.Height - videoH - bottomH
		if chatH < 0 {
			chatH = 0
		}
	}
	objects[0].Resize(fyne.NewSize(size.Width, videoH))
	objects[0].Move(fyne.NewPos(0, 0))
	objects[1].Resize(fyne.NewSize(size.Width, chatH))
	objects[1].Move(fyne.NewPos(0, videoH))
	objects[2].Resize(fyne.NewSize(size.Width, bottomH))
	objects[2].Move(fyne.NewPos(0, size.Height-bottomH))
}

func (l fixedChatLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	w := float32(0)
	for _, o := range objects {
		if o.MinSize().Width > w {
			w = o.MinSize().Width
		}
	}
	return fyne.NewSize(w, objects[0].MinSize().Height+l.chatH+objects[2].MinSize().Height)
}

// 测试模式标志（测试文件 TestMain 置 true 后复用 fyne test app）
var useTestApp bool

// runMainWindow 一体化窗口（视频 + 浮动对话 + 输入 + 底部栏）
func runMainWindow(exeDir string) {
	loadSettings(exeDir)
	loadMemory(exeDir)
	loadChatHistory(exeDir) // 对话历史持久化（重启接着聊）
	scanResources()
	buildActions() // 动作记忆区：从资源扫描生成（在 scanResources 之后）

	var a fyne.App
	if useTestApp {
		a = fyne.CurrentApp() // 测试环境复用 test app（window_test.go 注入）
	} else {
		a = app.NewWithID("fish.desktop.avatar")
	}
	w := a.NewWindow("小双 🐟")
	// 高度 = 视频270 + 对话75 + 底部~76，紧凑
	w.Resize(fyne.NewSize(480, 430))

	// ===== 视频区 =====
	videoImg := canvas.NewImageFromImage(nil)
	videoImg.FillMode = canvas.ImageFillContain
	videoImg.SetMinSize(fyne.NewSize(480, 270))
	globalVideoImg = videoImg
	player := NewVideoPlayer(videoImg)
	globalPlayer = player

	// 初始显示主图
	playMainStatic(player)

	// ===== 对话区（视频下方，约1-2行，可滚动）=====
	bubbleBox := container.NewVBox()
	bubbleScroll := container.NewVScroll(bubbleBox)
	bubbleScroll.SetMinSize(fyne.NewSize(480, 75))

	// 全局气泡函数（http /chat 转达等外部模块用；定义时立即赋值，不能等首次调用）
	globalAddMsg = func(r, t string) {
		fyneDo(func() {
			prefix := "🧑 你"
			if r == "assistant" {
				prefix = "🐟 小双"
			}
			// 半透明气泡
			bg := canvas.NewRectangle(color.NRGBA{R: 20, G: 20, B: 30, A: 200})
			content := container.NewVBox(
				widget.NewLabelWithStyle(prefix, fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				widget.NewLabel(t),
			)
			bubble := container.NewStack(bg, container.NewPadded(content))
			bubbleBox.Add(bubble)
			bubbleScroll.ScrollToBottom()
		})
	}
	addMsg := func(role, text string) {
		globalAddMsg(role, text)
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
		cmdMenu = widget.NewPopUpMenu(fyne.NewMenu("", exprItem, actItem), w.Canvas())
		testCmdMenu = cmdMenu
		// 注意：NewPopUpMenu 默认 OnDismiss = p.Hide()，这里覆盖时必须补回 Hide，
		// 否则点击外部/点选菜单项后菜单不会消失（fyne 只调用这一处 OnDismiss）
		cmdMenu.OnDismiss = func() {
			cmdMenu.Hide()
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
	langReady := false // 启动时 SetSelected 触发一次回调，跳过提示
	langSel := widget.NewSelect([]string{"中文", "English"}, func(v string) {
		settingsMu.Lock()
		if v == "English" {
			settings.System = "You are a cute and gentle girl named Xiaoshuang, a Pisces. Reply briefly and warmly, like a friend. Always reply in English, no matter what language the user writes in."
		} else {
			settings.System = "你是一个温柔可爱的少女，名字叫小双，是双鱼座。回复要简短自然，语气温柔亲切，像朋友一样聊天。无论对方用什么语言提问，都用中文回复。"
		}
		settingsMu.Unlock()
		saveSettings() // 持久化，重启不丢
		if langReady {
			addMsg("assistant", "🌍 已切换到"+v+"模式，之后我都会用"+v+"回复")
		}
	})
	langSel.SetSelected("中文")
	langReady = true
	setBtn := widget.NewButton("⚙️ 设置", func() { openSettingsDialog(w) })

	// ===== 语音按钮（按住说话）=====
	recBtn := newHoldButton("🎤 按住说话", startRecord, func() { stopRecordAndSend(addMsg, player) })

	bottomBar := container.NewHBox(menuBtn, recBtn, setBtn, langSel)

	// ===== 根布局：视频(顶,固定) + 对话(中,固定~1-2行) + 输入/设置(底,贴底) =====
	// Border center 会自动填满、VBox 会均分剩余空间，都不能固定对话区高度，用自定义布局
	root := container.New(fixedChatLayout{chatH: 75},
		videoImg,                              // 0: 视频固定顶部
		bubbleScroll,                          // 1: 对话区固定 75px（约1-2行）
		container.NewVBox(compose, bottomBar), // 2: 底部按自身高度贴底
	)
	w.SetContent(root)
	startTaskWorker() // 单线程任务队列（聊天/命令/总结串行）
	go startHTTPServer() // 本地 HTTP 命令接口（127.0.0.1:8721）
	go startIdleActions() // 空闲随机小动作
	go startRecallTimer() // 定时回忆
	go startTray(w) // 系统托盘（Linux 需 appindicator）

	// ===== 关窗保护：点 X → 隐藏到托盘（不退出）；托盘右键菜单可退出 =====
	w.SetCloseIntercept(func() {
		w.Hide() // 隐藏到托盘，程序继续常驻（HTTP/聊天/回忆都正常）
	})

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

// friendlyChatError 把 API/网络错误翻译成小双的口吻（人性化提醒）
func friendlyChatError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "api_not_configured"):
		return "我还不会说话呢～请在 ⚙️ 设置里填好 API 地址和 Key，我就能陪你聊天啦 🐟"
	case strings.Contains(msg, "401"), strings.Contains(msg, "InvalidApiKey"), strings.Contains(msg, "invalid api key"):
		return "主人～我的 API Key 好像不对或过期了，去 ⚙️ 设置里检查一下好吗 🙏"
	case strings.Contains(msg, "403"):
		return "我被拒绝访问了…去 ⚙️ 设置里确认一下 Key 的权限吧 😢"
	case strings.Contains(msg, "404"):
		return "我找不到这个模型…去 ⚙️ 设置里看看模型名写对了没 🤔"
	case strings.Contains(msg, "429"):
		return "我有点忙不过来（请求太多啦），稍等一下再试好不好～"
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "Timeout"), strings.Contains(msg, "EOF"), strings.Contains(msg, "connection refused"):
		return "网络好像不太顺畅，我连不上…过一会儿再试一次吧 😅"
	default:
		return "哎呀，我刚刚卡住了（" + msg + "），再试一次？"
	}
}

// handleMemoryCommand 处理记忆命令，返回(回复文本, 是否已处理)
// 窗口输入和 HTTP /chat 共用
func handleMemoryCommand(text string) (string, bool) {
	switch {
	case strings.HasPrefix(text, "/记 "):
		content := strings.TrimSpace(text[len("/记 "):])
		if content == "" {
			return "要记住什么呀？格式：/记 内容", true
		}
		addMem(content, "important")
		return "📝 记住啦：「" + content + "」（已放入重要记忆）", true
	case text == "/记忆":
		return listMemory(), true
	case strings.HasPrefix(text, "/忘 "):
		kw := strings.TrimSpace(text[len("/忘 "):])
		if kw == "" {
			return "想忘掉什么？格式：/忘 关键词", true
		}
		if n := removeMemory(kw); n > 0 {
			return fmt.Sprintf("🗑️ 忘掉了 %d 条相关记忆", n), true
		}
		return "没有找到包含「" + kw + "」的记忆", true
	}
	return "", false
}

// sendChat 发送文字对话（播放器联动表情 + 记忆命令 + 自动总结）
func sendChat(text string, addMsg func(string, string), player *VideoPlayer) {
	trimmed := strings.TrimSpace(text)
	maybeTick() // 时间流转：单天→一周→更远（每小时最多一次）

	// ===== 记忆命令 =====
	if reply, handled := handleMemoryCommand(trimmed); handled {
		addMsg("assistant", reply)
		return
	}

	// ===== 触发词表演（秒响应，不走 AI）=====
	if act := matchTrigger(trimmed); act != "" {
		if _, ok := actionFiles[act]; ok {
			playAction(act, player)
		} else {
			playExpr(act, player)
		}
		addMsg("assistant", "🎭 来啦～")
		return
	}

	// 记录用户消息（持久化）
	appendChat("user", text)
	addMsg("user", text)
	setMoodNow("think", player)
	scheduleSummarize() // 用户发言 → 重置 5 分钟静默计时器

	// 忙时提示：小双正在干活（回复/执行命令），消息不入队，请稍后重发
	if isTaskBusy() {
		addMsg("assistant", "🫥 小双正在忙，等我一下下～")
		setMoodNow("idle", player)
		return
	}

	// AI 回复排队（单线程串行：一次只处理一条消息）
	enqueue(func() {
		h := recentHistory(chatAICtxMax) // 最近 50 条上下文
		reply, err := chatWithAI(h)
		isErr := err != nil
		if isErr {
			reply = friendlyChatError(err)
		}
		// 解析 AI 选择的表演动作/表情
		cleanReply, actName := extractAction(reply)
		if !isErr && actName != "" {
			playExtractedAction(actName, player)
		}
		addMsg("assistant", cleanReply)
		appendChat("assistant", cleanReply) // 持久化
		if !isErr {
			setMoodNow("happy", player)
		} else {
			setMoodNow("sad", player)
		}
	})
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
		appendChat("user", text) // 持久化
		scheduleSummarize()      // 语音发言也算对话活跃

		setMoodNow("think", player)
		// 忙时提示：小双正在干活，语音消息不入队
		if isTaskBusy() {
			addMsg("assistant", "🫥 小双正在忙，等我一下下～")
			setMoodNow("idle", player)
			return
		}
		// AI 回复排队串行（不阻塞录音流程，但回复和主对话互斥）
		enqueue(func() {
			h := recentHistory(chatAICtxMax)
			reply, err := chatWithAI(h)
			isErr := err != nil
			if isErr {
				reply = friendlyChatError(err)
			}
			// 解析 AI 选择的表演动作/表情
			cleanReply, actName := extractAction(reply)
			if !isErr && actName != "" {
				playExtractedAction(actName, player)
			}
			addMsg("assistant", cleanReply)
			appendChat("assistant", cleanReply) // 持久化
			if isErr {
				setMoodNow("sad", player)
				return
			}
			setMoodNow("happy", player)

			if !strings.HasPrefix(reply, "⚠️") {
				mp3 := strings.TrimSuffix(f, ".wav") + ".mp3"
				if err := ttsEdge(reply, mp3); err == nil {
					playAudio(mp3)
				}
			}
		})
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
	// 不能 Disable()：灰了之后用户以为不可用；按住逻辑靠 MouseDown/MouseUp 驱动
	return widget.NewSimpleRenderer(btn)
}

var holdPressed bool

// Tapped 空实现：按住逻辑由 MouseDown/MouseUp 驱动，避免点击松开时双触发 onRelase
func (b *holdButton) Tapped(_ *fyne.PointEvent) {}

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
