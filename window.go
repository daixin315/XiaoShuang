package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// 全局视频播放器（daemonLoop 和窗口共用）
var globalPlayer *VideoPlayer

// 全局窗口引用（判断可见性：隐藏到托盘时弹独立文本框）
var globalWindow fyne.Window

// globalWinVisible 主窗口是否可见（fyne 无 Visible()，自己维护；UI 线程内访问）
var globalWinVisible = true

// floatingWin 浮动提示窗（单例：有则更新内容，无则创建）
var floatingWin fyne.Window

// showFloatingMsg 弹独立文本框（窗口隐藏到托盘时替代气泡；Entry 支持选中复制+自动换行）
func showFloatingMsg(text string) {
	fyneDo(func() {
		if floatingWin == nil {
			floatingWin = fyne.CurrentApp().NewWindow("🐟 小双")
			floatingWin.SetOnClosed(func() { floatingWin = nil })
		}
		msgEntry := widget.NewEntry()
		msgEntry.MultiLine = true
		msgEntry.SetText(text)
		msgEntry.Wrapping = fyne.TextWrapBreak
		floatingWin.SetContent(container.NewVBox(
			msgEntry,
			container.NewHBox(
				layout.NewSpacer(),
				widget.NewButton("知道了", func() {
					if floatingWin != nil {
						floatingWin.Close()
					}
				}),
			),
		))
		floatingWin.Resize(fyne.NewSize(430, 220))
		floatingWin.Show()
		floatingWin.RequestFocus()
	})
}

// 全局视频显示对象（playMainStatic 回主图用）
var globalVideoImg *canvas.Image

// globalAddMsg 全局对话气泡函数（runMainWindow 赋值；http 接口等外部模块用）
var globalAddMsg func(role, text string)

// 测试句柄（fyne test 驱动 UI 用；生产无影响）
var (
	testInput   *widget.Entry
	testCmdMenu *widget.PopUp
)

// ChatLog 对话记录（内存）
var (
	chatHistory []ChatMessage
	chatLogMu   sync.Mutex
)

// 静默总结：一轮对话 = 用户停止发言 5 分钟；到点总结新增消息
var (
	lastSummaryIdx int
)

// scheduleSummarize 立即总结新增对话（AI 回复后调用，不再等 5 分钟静默）
// 用户之前要求"不需要5分钟间隔"；且 5 分钟定时器会被频繁发言一直重置导致永不总结（记忆为空的根因）
func scheduleSummarize() {
	chatLogMu.Lock()
	start := lastSummaryIdx
	if start > len(chatHistory) {
		start = 0 // 历史被截断过，从头总结
	}
	newMsgs := append([]ChatMessage{}, chatHistory[start:]...)
	lastSummaryIdx = len(chatHistory)
	chatLogMu.Unlock()
	if len(newMsgs) >= 2 { // 至少一问一答才算一轮
		fmt.Printf("[memory] 总结 %d 条新消息\n", len(newMsgs))
		enqueue(func() { summarizeForMemory(newMsgs) }) // 排队串行，不和主对话抢
	}
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
	loadTodos(exeDir)       // 待办清单
	scanResources()
	buildActions() // 动作记忆区：从资源扫描生成（在 scanResources 之后）

	var a fyne.App
	if useTestApp {
		a = fyne.CurrentApp() // 测试环境复用 test app（window_test.go 注入）
	} else {
		a = app.NewWithID("fish.desktop.avatar")
	}
	w := a.NewWindow("小双 🐟")
	a.Settings().SetTheme(theme.DarkTheme()) // 强制暗色主题：白字+深色控件统一，避免浅色主题下黑字配深色气泡看不清
	// 高度贴合视频(270) + 底部按钮(≈40)，无上下留白
	w.Resize(fyne.NewSize(480, 310))
	chatWin := a.NewWindow("💬 小双对话") // 对话窗口（独立，双窗口模式）

	// ===== 视频区 =====
	videoImg := canvas.NewImageFromImage(nil)
	videoImg.FillMode = canvas.ImageFillContain // 随窗口缩放，保持比例不变形（无 MinSize，可任意缩小）
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
			// 窗口隐藏到托盘时 → 弹独立文本框（气泡看不见）
			if globalWindow != nil && !globalWinVisible {
				showFloatingMsg(prefix + "：" + t)
				return
			}
			// 半透明气泡
			bg := canvas.NewRectangle(color.NRGBA{R: 20, G: 20, B: 30, A: 200})
			// 消息文本用 RichText：自动换行 + 高度随内容（颜色跟随强制暗色主题=白字）
			seg := &widget.TextSegment{Text: t}
			rt := widget.NewRichText(seg)
			rt.Wrapping = fyne.TextWrapBreak
			// 复制按钮（RichText 不可选中复制，用按钮补偿）
			copyBtn := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() {
				w.Clipboard().SetContent(t)
			})
			copyBtn.Importance = widget.LowImportance
			// 标题用小字号（默认字高一半左右，紧凑）
			title := canvas.NewText(prefix, color.NRGBA{R: 255, G: 255, B: 255, A: 220})
			title.TextSize = 10
			title.TextStyle = fyne.TextStyle{Bold: true}
			content := container.NewVBox(
				container.NewHBox(title, copyBtn),
				rt,
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
	// 两级导航菜单：😊表情 / 🎬动作 → 点开对应分组列表（滚动全部可见，带←返回）
	var cmdMenu *widget.PopUp
	var cmdScroll *container.Scroll
	var cmdLastPos fyne.Position
	var buildCmdMenu func(c fyne.Canvas, level int, availH float32)
	buildCmdMenu = func(c fyne.Canvas, level int, availH float32) {
		items := []fyne.CanvasObject{}
		openLevel := func(l int) {
			cmdMenu.Hide()
			buildCmdMenu(c, l, availH)
			cmdMenu.ShowAtPosition(cmdLastPos)
		}
		if level == 0 {
			// 主菜单：两个分组入口（点外部即可关闭，无需关闭按钮）
			items = append(items, widget.NewButton("😊 表情", func() { openLevel(1) }))
			items = append(items, widget.NewButton("🎬 动作", func() { openLevel(2) }))
		} else {
			// 分组列表：标题 + 全部项（点外部/点项后即关闭，无需返回按钮）
			if level == 1 {
				title := canvas.NewText("😊 表情", color.NRGBA{R: 255, G: 220, B: 120, A: 255})
				title.TextSize = 12
				title.TextStyle = fyne.TextStyle{Bold: true}
				items = append(items, title)
				for _, n := range exprNames {
					n := n
					items = append(items, widget.NewButton("😊 "+n, func() {
						cmdMenu.Hide()
						if strings.HasPrefix(strings.TrimSpace(input.Text), "/") {
							input.SetText("")
						}
						playExpr(n, player)
					}))
				}
			} else {
				title := canvas.NewText("🎬 动作", color.NRGBA{R: 120, G: 200, B: 255, A: 255})
				title.TextSize = 12
				title.TextStyle = fyne.TextStyle{Bold: true}
				items = append(items, title)
				for _, n := range actionNames {
					n := n
					items = append(items, widget.NewButton("🎬 "+n, func() {
						cmdMenu.Hide()
						if strings.HasPrefix(strings.TrimSpace(input.Text), "/") {
							input.SetText("")
						}
						playAction(n, player)
					}))
				}
			}
		}
		cmdScroll = container.NewVScroll(container.NewVBox(items...))
		cmdScroll.SetMinSize(fyne.NewSize(240, availH))
		cmdMenu = widget.NewPopUp(cmdScroll, c)
		testCmdMenu = cmdMenu
	}
	showCmdMenu := func(c fyne.Canvas, anchor fyne.CanvasObject) {
		fmt.Println("[menu] showCmdMenu 调用")
		if cmdMenu != nil {
			cmdMenu.Hide()
		}
		// 弹出即清空 "/" 触发词（PopUp 无 OnDismiss，避免点外部关闭后残留）
		if strings.HasPrefix(strings.TrimSpace(input.Text), "/") {
			input.SetText("")
		}
		// 高度 = 窗口高度 - 上下边距（菜单居中显示，完整可见，不依赖按钮位置）
		size := c.Size()
		availH := size.Height - 40
		if availH > 400 {
			availH = 400
		}
		if availH < 150 {
			availH = 150
		}
		buildCmdMenu(c, 0, availH)
		// 位置：窗口内居中（宽 250 的菜单）
		cmdLastPos = fyne.NewPos((size.Width-250)/2, (size.Height-availH)/2)
		cmdMenu.ShowAtPosition(cmdLastPos)
	}
	input.OnChanged = func(text string) {
		if strings.TrimSpace(text) == "/" {
			fmt.Println("[menu] 输入 / 触发菜单")
			showCmdMenu(chatWin.Canvas(), input)
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
			showCmdMenu(chatWin.Canvas(), input) // 输入 / 后直接回车也弹菜单
			return
		}
		sendBtn.OnTapped()
	}
	compose := container.NewBorder(nil, nil, nil, sendBtn, input)

	// ===== 底部栏（显示小双 + 帮助 + 设置；表情/动作按钮在视频窗口）=====
	var menuBtn *widget.Button
	menuBtn = widget.NewButton("🎭 表情/动作", func() { showCmdMenu(w.Canvas(), menuBtn) })
	// 🐟 显示小双（点击显示视频窗口，不切换）
	showMascotBtn := widget.NewButton("🐟 显示小双", func() {
		w.Show()
		w.RequestFocus()
	})
	setBtn := widget.NewButton("⚙️ 设置", func() { openSettingsDialog(chatWin) })

	// 🔍 帮助（一次性）：点击立即分析当前桌面并给出建议，只分析一次
	onceHelpBtn := widget.NewButton("🔍 帮助", func() {
		globalAddMsg("assistant", "🔍 我看看你屏幕…")
		helperOnce()
	})
	// 💡 实时帮助（持续观察模式）：截图看用户在干嘛，需要帮助才提醒
	var helpBtn *widget.Button
	helpBtn = widget.NewButton("💡 实时帮助", func() {
		if isHelperActive() {
			toggleHelper(false)
			helpBtn.SetText("💡 实时帮助")
		} else {
			toggleHelper(true)
			helpBtn.SetText("🟢 实时帮助中")
		}
	})

	bottomBar := container.NewHBox(showMascotBtn, onceHelpBtn, helpBtn, setBtn)

	// ===== 根布局：视频(顶,固定) + 对话(中,固定~1-2行) + 输入/设置(底,贴底) =====
	// Border center 会自动填满、VBox 会均分剩余空间，都不能固定对话区高度，用自定义布局
	// ===== 双独立窗口：视频窗口（形象）+ 对话窗口 =====
	showChatBtn := widget.NewButton("💬 显示对话", func() {
		chatWin.Show()
		chatWin.RequestFocus()
	})
	w.SetContent(container.NewBorder(
		nil,
		container.NewHBox(menuBtn, showChatBtn),
		nil, nil,
		videoImg,
	))
	// 对话窗口：对话区 + 输入 + 底部栏（"显示小双"在左下角）
	chatWin.SetContent(container.NewBorder(
		nil,
		container.NewVBox(compose, bottomBar),
		nil, nil,
		bubbleScroll, // 对话区填满
	))
	chatWin.Resize(fyne.NewSize(460, 340))               // 与视频窗口同高
	chatWin.SetCloseIntercept(func() { chatWin.Hide() }) // 关闭=隐藏，按钮可再开
	chatWin.Hide()                                       // 初始只显示视频窗口
	startTaskWorker()                                    // 单线程任务队列（聊天/命令/总结串行）
	go startHTTPServer()                                 // 本地 HTTP 命令接口（127.0.0.1:8721）
	go startIdleActions()                                // 空闲随机小动作
	go startRecallTimer()                                // 定时回忆
	go startTodoLoop()                                   // 待办检查（到点提醒）
	if os.Getenv("FISH_NO_TRAY") == "" {
		go startTray(w) // 系统托盘（Linux 需 appindicator）；FISH_NO_TRAY=1 跳过（排查用）
	}

	// ===== 关窗保护：点 X → 隐藏到托盘（不退出）；托盘右键"显示窗口"恢复 =====
	// 全平台统一（Windows 托盘右键菜单同样有"显示窗口"）
	globalWindow = w
	w.SetCloseIntercept(func() {
		globalWinVisible = false
		w.Hide() // 隐藏到托盘，程序继续常驻（HTTP/聊天/回忆都正常）
	})

	w.ShowAndRun()
}

// undecorateVideoWindow Linux 无边框 hack：fyne 无 SetDecorated API，
// 用 xprop _MOTIF_WM_HINTS 去掉视频窗口标题栏（GNOME Wayland 对 Xwayland 窗口生效）
func undecorateVideoWindow() {
	go func() {
		time.Sleep(3 * time.Second) // 等窗口创建完成
		script := `for id in $(DISPLAY=:0 xdotool search --name "小双" 2>/dev/null); do
  geo=$(DISPLAY=:0 xdotool getwindowgeometry $id 2>/dev/null | grep Geometry)
  case "$geo" in *480x340*) DISPLAY=:0 xprop -id $id -f _MOTIF_WM_HINTS 32c -set _MOTIF_WM_HINTS "0x2, 0x0, 0x0, 0x0, 0x0" 2>/dev/null;; esac
done`
		exec.Command("sh", "-c", script).Run()
	}()
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

	// ===== 待办命令（记一下/提醒我 → AI 解析时间）=====
	if reply, handled := handleTodoCommand(trimmed); handled {
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

	// ===== 情绪识别（伤心/开心 → 自动换表情）=====
	applyMoodReaction(detectMood(trimmed), player)

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
		scheduleSummarize()                 // 回复完成 → 立即总结本轮对话进记忆
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

// saveChatModel 保存对话模型设置（对话模型对话框）
func saveChatModel(baseURL, apiKey, model string) {
	settingsMu.Lock()
	settings.BaseURL = strings.TrimSpace(baseURL)
	settings.APIKey = strings.TrimSpace(apiKey)
	settings.Model = strings.TrimSpace(model)
	settingsMu.Unlock()
	if err := saveSettings(); err != nil {
		fmt.Printf("[settings] 保存失败: %v\n", err)
	}
}

// saveVisionModel 保存视觉模型设置（视觉模型对话框）
func saveVisionModel(baseURL, apiKey, model string) {
	settingsMu.Lock()
	settings.VisionBaseURL = strings.TrimSpace(baseURL)
	settings.VisionAPIKey = strings.TrimSpace(apiKey)
	settings.VisionModel = strings.TrimSpace(model)
	settingsMu.Unlock()
	if err := saveSettings(); err != nil {
		fmt.Printf("[settings] 保存失败: %v\n", err)
	}
}

// openModelDialog 模型设置对话框：地址/Key/模型 三输入 + 取消/保存（模型配置无内置默认，用户自填）
func openModelDialog(parent fyne.Window, title, curBase, curKey, curModel string, save func(string, string, string)) {
	baseE := widget.NewEntry()
	baseE.SetText(curBase)
	keyE := widget.NewPasswordEntry()
	keyE.SetText(curKey)
	modelE := widget.NewEntry()
	modelE.SetText(curModel)

	var d *dialog.CustomDialog
	cancelBtn := widget.NewButton("取消", func() {
		if d != nil {
			d.Hide()
		}
	})
	saveBtn := widget.NewButton("保存", func() {
		save(baseE.Text, keyE.Text, modelE.Text)
		if d != nil {
			d.Hide()
		}
	})
	content := container.NewVBox(
		widget.NewLabel("API 地址"), baseE,
		widget.NewLabel("API Key"), keyE,
		widget.NewLabel("模型"), modelE,
		container.NewHBox(cancelBtn, saveBtn),
	)
	d = dialog.NewCustom(title, "", content, parent)
	d.Resize(fyne.NewSize(460, 300))
	d.Show()
}

// ---------- 设置窗口 ----------
// setSettingString 按字段名更新设置并保存（提示词对话框用）
func setSettingString(field, val string) {
	settingsMu.Lock()
	switch field {
	case "System":
		settings.System = val
	case "HelpOncePrompt":
		settings.HelpOncePrompt = val
	case "HelpLivePrompt":
		settings.HelpLivePrompt = val
	}
	settingsMu.Unlock()
	if err := saveSettings(); err != nil {
		fmt.Printf("[settings] 保存失败: %v\n", err)
	}
}

// openPromptDialog 提示词编辑对话框：取消/默认/保存（默认=恢复内置原始文本）
func openPromptDialog(parent fyne.Window, title, current, def string, save func(string)) {
	entry := widget.NewMultiLineEntry()
	entry.SetText(current)
	entry.Wrapping = fyne.TextWrapBreak
	var d *dialog.CustomDialog
	cancelBtn := widget.NewButton("取消", func() {
		if d != nil {
			d.Hide()
		}
	})
	defBtn := widget.NewButton("默认", func() { entry.SetText(def) })
	saveBtn := widget.NewButton("保存", func() {
		save(entry.Text)
		if d != nil {
			d.Hide()
		}
	})
	content := container.NewVBox(
		entry,
		container.NewHBox(cancelBtn, defBtn, saveBtn),
	)
	d = dialog.NewCustom(title, "", content, parent)
	d.Resize(fyne.NewSize(500, 340))
	d.Show()
}

func openSettingsDialog(parent fyne.Window) {
	settingsMu.RLock()
	s := settings
	settingsMu.RUnlock()

	// 2 个模型设置按钮（各自弹对话框：取消/保存；模型配置用户自填，视觉模型预填默认值）
	modelBtns := container.NewHBox(
		widget.NewButton("💬 对话模型", func() {
			openModelDialog(parent, "💬 对话模型", s.BaseURL, s.APIKey, s.Model, saveChatModel)
		}),
		widget.NewButton("👁️ 视觉模型", func() {
			// 首次打开预填默认（地址=deepseek、模型=vision-exp），用户只需填 Key
			vBase := s.VisionBaseURL
			if vBase == "" {
				vBase = "https://api.deepseek.com/v1"
			}
			vModel := s.VisionModel
			if vModel == "" {
				vModel = "deepseek-v4-flash-vision-exp"
			}
			openModelDialog(parent, "👁️ 视觉模型", vBase, s.VisionAPIKey, vModel, saveVisionModel)
		}),
	)

	helpInt := widget.NewEntry()
	helpInt.SetText(fmt.Sprintf("%d", helperInterval()))

	// 3 个提示词设置按钮（各自弹对话框：取消/默认/保存）
	promptBtns := container.NewHBox(
		widget.NewButton("✏️ 基本人设", func() {
			openPromptDialog(parent, "✏️ 基本人设", s.System, defaultSettings().System, func(t string) { setSettingString("System", t) })
		}),
		widget.NewButton("✏️ 帮助设置", func() {
			openPromptDialog(parent, "✏️ 帮助设置", helperOncePrompt(), visionPrompt, func(t string) { setSettingString("HelpOncePrompt", t) })
		}),
		widget.NewButton("✏️ 实时帮助设置", func() {
			openPromptDialog(parent, "✏️ 实时帮助设置", helperLivePrompt(), visionPrompt, func(t string) { setSettingString("HelpLivePrompt", t) })
		}),
	)

	form := dialog.NewForm("⚙️ 设置", "保存", "取消",
		[]*widget.FormItem{
			widget.NewFormItem("模型设置", modelBtns),
			widget.NewFormItem("帮助观察间隔(秒)", helpInt),
			widget.NewFormItem("提示词设置", promptBtns),
		},
		func(ok bool) {
			if !ok {
				return
			}
			iv, _ := strconv.Atoi(strings.TrimSpace(helpInt.Text))
			if iv <= 0 {
				iv = helperDefaultInt
			}
			settingsMu.Lock()
			settings.HelpInterval = iv
			settingsMu.Unlock()
			if err := saveSettings(); err != nil {
				fmt.Printf("[settings] 保存失败: %v\n", err)
				dialog.ShowError(fmt.Errorf("设置保存失败：%v\n路径：%s", err, settingsPath), parent)
				return
			}
			fmt.Printf("[settings] 已保存到 %s (模型=%s)\n", settingsPath, settings.Model)
			dialog.ShowInformation("✅ 已保存", "设置已保存。\n"+settingsPath, parent)
		}, parent)
	form.Resize(fyne.NewSize(420, 520))
	form.Show()
}
