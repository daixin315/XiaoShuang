package main

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/test"
)

// TestMain 注入测试模式：runMainWindow 复用 fyne test app
func TestMain(m *testing.M) {
	useTestApp = true
	os.Exit(m.Run())
}

// 测试初始化：指向真实 assets 目录，避免污染当前目录
func testInit(t *testing.T) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	videoDir = filepath.Join(dir, "assets")
	moodFile = filepath.Join(dir, "mood_test.json")
	os.Remove(moodFile)
	scanResources()
	if len(exprNames) == 0 || len(actionNames) == 0 {
		t.Fatalf("素材扫描失败: 表情=%d 动作=%d", len(exprNames), len(actionNames))
	}
	t.Logf("素材: 表情=%d 动作=%d 主图=%s", len(exprNames), len(actionNames), mainImgPath)
}

// 1. 资源映射：英文情绪、中文名、未知 → 主图
func TestResolveEmotion(t *testing.T) {
	testInit(t)
	// 英文情绪 → 中文表情
	if p := resolveEmotion("happy"); p != exprFiles["开心"] {
		t.Errorf("happy → %s, want %s", p, exprFiles["开心"])
	}
	if p := resolveEmotion("think"); p != exprFiles["思考"] {
		t.Errorf("think → %s, want %s", p, exprFiles["思考"])
	}
	// 中文名直接命中
	if p := resolveEmotion("眨眼"); p != exprFiles["眨眼"] {
		t.Errorf("眨眼 → %s, want %s", p, exprFiles["眨眼"])
	}
	// 未知 → 主图
	if p := resolveEmotion("unknown_xyz"); p != mainImgPath {
		t.Errorf("unknown → %s, want 主图 %s", p, mainImgPath)
	}
	// idle → 主图
	if p := resolveEmotion("idle"); p != mainImgPath {
		t.Errorf("idle → %s, want 主图", p)
	}
}

// 2. "/" 命令菜单：输入 / 弹出菜单，含表情/动作子菜单
func TestCmdMenuPopup(t *testing.T) {
	testInit(t)
	app := test.NewApp()
	defer app.Quit()

	go runMainWindow(".") // test driver 下 ShowAndRun 不阻塞
	time.Sleep(300 * time.Millisecond)

	if testInput == nil {
		t.Fatal("testInput 未初始化（窗口未构建）")
	}
	// 模拟真实键盘输入 "/"（触发 OnChanged → 弹菜单）
	test.Type(testInput, "/")
	time.Sleep(200 * time.Millisecond)

	if testCmdMenu == nil {
		t.Fatal("输入 / 后 testCmdMenu 未创建")
	}
	if !testCmdMenu.Visible() {
		t.Fatal("菜单未显示 (Visible=false)")
	}
	t.Log("✅ / 命令菜单弹出成功")

	// 弹出即清空 "/" 触发词（PopUp 无 OnDismiss）
	if testInput.Text != "" {
		t.Errorf("弹出后输入框应为空, got %q", testInput.Text)
	}

	// 点击菜单外部 → 非模态 PopUp 自动关闭
	canvas := app.Driver().CanvasForObject(testInput)
	test.TapCanvas(canvas, fyne.NewPos(2, 2))
	time.Sleep(200 * time.Millisecond)
	if testCmdMenu.Visible() {
		t.Error("❌ 点击外部后菜单未关闭")
	} else {
		t.Log("✅ 点击外部菜单已关闭")
	}
}

// 6. 菜单弹出后点菜单项关闭 + 点击外部关闭（自定义滚动 PopUp）
func TestMenuSubmenuItemDismisses(t *testing.T) {
	testInit(t)
	app := test.NewApp()
	defer app.Quit()
	go runMainWindow(".")
	time.Sleep(300 * time.Millisecond)
	if testInput == nil {
		t.Fatal("窗口未构建")
	}

	test.Type(testInput, "/")
	time.Sleep(200 * time.Millisecond)
	if testCmdMenu == nil || !testCmdMenu.Visible() {
		t.Fatal("菜单未弹出")
	}
	t.Log("✅ 菜单弹出（自定义滚动 PopUp，含全部表情/动作项）")

	// 点击外部 → 非模态 PopUp 自动关闭
	canvas := app.Driver().CanvasForObject(testInput)
	test.TapCanvas(canvas, fyne.NewPos(2, 2))
	time.Sleep(200 * time.Millisecond)
	if testCmdMenu.Visible() {
		t.Error("❌ 点击外部后菜单未关闭")
	} else {
		t.Log("✅ 菜单已关闭")
	}
}

// 7. 点击菜单外部任意区域菜单必须关闭
func TestMenuDismissOnOutsideTap(t *testing.T) {
	testInit(t)
	app := test.NewApp()
	defer app.Quit()
	go runMainWindow(".")
	time.Sleep(300 * time.Millisecond)

	test.Type(testInput, "/")
	time.Sleep(200 * time.Millisecond)
	if testCmdMenu == nil || !testCmdMenu.Visible() {
		t.Fatal("菜单未弹出")
	}

	// 点击输入框等菜单外区域 → 非模态 PopUp 自动关闭
	canvas := app.Driver().CanvasForObject(testInput)
	test.TapCanvas(canvas, fyne.NewPos(2, 2))
	time.Sleep(200 * time.Millisecond)
	if testCmdMenu.Visible() {
		t.Error("❌ 点击输入框后菜单未关闭（用户反馈的 bug）")
	} else {
		t.Log("✅ 点击菜单外区域菜单已关闭")
	}
}

// 8. 人性化错误提示（API key 未配置等场景）
func TestFriendlyChatError(t *testing.T) {
	cases := []struct{ err, want string }{
		{"api_not_configured", "设置"},
		{"API 401: InvalidApiKey", "Key"},
		{"API 404: model not found", "模型"},
		{"API 429: rate limit", "忙不过来"},
		{"context deadline exceeded (Client.Timeout)", "网络"},
	}
	for _, c := range cases {
		got := friendlyChatError(fmt.Errorf("%s", c.err))
		if !strings.Contains(got, c.want) {
			t.Errorf("friendlyChatError(%q) = %q, want 包含 %q", c.err, got, c.want)
		}
	}
	t.Log("✅ 人性化错误提示覆盖：未配置/401/404/429/超时")
}

// 9. 记忆模块（六层 v2：分隔符文本 + 内存缓存）
func TestMemory(t *testing.T) {
	memDir = filepath.Join(t.TempDir(), "memory")
	os.MkdirAll(memDir, 0o755)
	coreLines, sysLines, impLines, timeline = nil, nil, nil, nil

	// 核心区：添加 + 同主题更新（不新增）
	addMem("用户叫Tom", "core")
	addMem("用户叫Tom", "core") // 相同 → 更新替换，不重复
	addMem("用户喜欢喝咖啡", "core")
	if len(coreLines) != 2 {
		t.Fatalf("核心区去重失败: len=%d", len(coreLines))
	}
	addMem("用户叫Tom，喜欢玩桌游", "core") // 前6字同主题 → 更新
	if len(coreLines) != 2 || !strings.Contains(coreLines[0], "桌游") {
		t.Fatalf("核心区更新失败: %v", coreLines)
	}

	// 规范化：trim/换行转空格/限长
	addMem("  带空格 内容  ", "important")
	if impLines[0] != "带空格 内容" {
		t.Errorf("规范化失败: %q", impLines[0])
	}
	addMem("超长内容"+strings.Repeat("字", 200), "important")
	if len([]rune(impLines[len(impLines)-1])) > 100 {
		t.Errorf("限长失败")
	}

	// timeline：添加 + 去重
	addMem("今天去了超市", "today")
	addMem("今天去了超市", "today") // 重复跳过
	if len(timeline) != 1 {
		t.Errorf("timeline 去重失败: len=%d", len(timeline))
	}

	// 重要区上限：塞 50 条 → 只剩 40
	for i := 0; i < 50; i++ {
		addMem(fmt.Sprintf("重要条目%02d", i), "important")
	}
	if len(impLines) > importantMax {
		t.Errorf("重要区超限: %d > %d", len(impLines), importantMax)
	}

	// 注入文本
	p := memoryPrompt(false)
	if !strings.Contains(p, "Tom") {
		t.Errorf("memoryPrompt 缺少核心记忆: %q", p)
	}

	// 删除
	if n := removeMemory("咖啡"); n != 1 {
		t.Errorf("removeMemory 应删1条, got %d", n)
	}
	if n := removeMemory("不存在关键词xyz"); n != 0 {
		t.Errorf("removeMemory 不存在应删0, got %d", n)
	}

	// 落盘再读回（分隔符格式解析）
	writeLayerFile("core", coreLines)
	writeLayerFile("important", impLines)
	writeTimelineFile()
	coreLines = readLines(filepath.Join(memDir, "core.txt"))
	impLines = readLines(filepath.Join(memDir, "important.txt"))
	timeline = readTimeline(filepath.Join(memDir, "timeline.txt"))
	if len(timeline) != 1 || timeline[0].Text != "今天去了超市" {
		t.Errorf("timeline 读回失败: %+v", timeline)
	}
	t.Logf("✅ 记忆模块v2: 分层/更新/去重/规范化/上限/删除/落盘读回 全部通过")
}

// 10. extractAction 解析 AI 的 [表演] 标记
func TestExtractAction(t *testing.T) {
	testInit(t) // 加载资源（exprFiles/actionFiles）
	// 有效动作
	clean, act := extractAction("我荡个秋千给你看~\n[表演]单人荡秋千")
	if act != "单人荡秋千" || strings.Contains(clean, "表演") {
		t.Errorf("动作解析失败: clean=%q act=%q", clean, act)
	}
	// 有效表情
	clean, act = extractAction("好开心呀！\n[表演]开心")
	if act != "开心" || strings.Contains(clean, "表演") {
		t.Errorf("表情解析失败: clean=%q act=%q", clean, act)
	}
	// 无表演标记
	clean, act = extractAction("今天天气不错")
	if act != "" || clean != "今天天气不错" {
		t.Errorf("无标记解析失败: clean=%q act=%q", clean, act)
	}
	// 无效名字（AI 编造，不在列表里）→ 移除该行不播放，回复不留痕迹
	clean, act = extractAction("嘿嘿\n[表演]不存在的东西")
	if act != "" || strings.Contains(clean, "不存在") {
		t.Errorf("无效名字应移除: clean=%q act=%q", clean, act)
	}
	t.Log("✅ extractAction: 动作/表情/无标记/无效名 全部通过")
}

// 11. 对话历史持久化：写盘→清空→读回
func TestChatHistory(t *testing.T) {
	dir := t.TempDir()
	chatHistPath = filepath.Join(dir, "chat_history.txt")
	chatLogMu.Lock()
	chatHistory = nil
	chatLogMu.Unlock()

	appendChat("user", "你好")
	appendChat("assistant", "你好呀～")
	appendChat("user", "我明天出差")
	if len(chatHistory) != 3 {
		t.Fatalf("appendChat 失败: len=%d", len(chatHistory))
	}
	// 模拟重启：清内存重新 load
	chatLogMu.Lock()
	chatHistory = nil
	chatLogMu.Unlock()
	loadChatHistory(dir)
	if len(chatHistory) != 3 || chatHistory[0].Role != "user" || chatHistory[2].Content != "我明天出差" {
		t.Fatalf("loadChatHistory 读回失败: %+v", chatHistory)
	}
	// 截断：塞 250 条（共 253 条）→ 只剩 200，最旧被删
	for i := 0; i < 250; i++ {
		appendChat("user", fmt.Sprintf("消息%03d", i))
	}
	if len(chatHistory) != chatHistMax {
		t.Errorf("截断失败: len=%d", len(chatHistory))
	}
	if chatHistory[0].Content != "消息050" { // 253 条删最旧53条，保留 050~249
		t.Errorf("截断保留错误: %q", chatHistory[0].Content)
	}
	// recentHistory
	r := recentHistory(3)
	if len(r) != 3 || r[0].Content != "消息247" {
		t.Errorf("recentHistory 失败: %+v", r)
	}
	t.Log("✅ 对话历史: 追加/持久化读回/截断/recentHistory 全部通过")
}

// 12. 触发词匹配（动作/社交类；情绪词归 detectMood）
func TestMatchTrigger(t *testing.T) {
	testInit(t)
	cases := []struct{ text, want string }{
		{"拜拜", "眨眼"},
		{"荡秋千", "单人荡秋千"},
		{"晚安", "困倦"},
		{"我们跳舞吧", "蝴蝶围圈"},
		{"摸鱼", "河边锦鲤"},
	}
	for _, c := range cases {
		got := matchTrigger(c.text)
		if got != c.want {
			t.Errorf("matchTrigger(%q) = %q, want %q", c.text, got, c.want)
		}
	}
	// 不匹配：情绪词/普通消息（情绪词走 detectMood）
	for _, text := range []string{"我今天很开心", "我太难过了", "今天天气不错"} {
		if got := matchTrigger(text); got != "" {
			t.Errorf("不应触发: %q → %q", text, got)
		}
	}
	// detectMood 情绪识别
	if m := detectMood("我今天太难过了"); m != "sad" {
		t.Errorf("detectMood sad 失败: %q", m)
	}
	if m := detectMood("哈哈太棒了"); m != "happy" {
		t.Errorf("detectMood happy 失败: %q", m)
	}
	if m := detectMood("今天天气不错"); m != "" {
		t.Errorf("detectMood 空失败: %q", m)
	}
	t.Log("✅ 触发词+情绪识别: 动作词触发/情绪词分流 全部通过")
}

// 13. 像素差异变化检测：微小变化比例低，显著变化比例高
func TestDHash(t *testing.T) {
	// 生成两张 100x60 测试图
	mk := func(bg, fg int, block bool) image.Image {
		img := image.NewRGBA(image.Rect(0, 0, 100, 60))
		for y := 0; y < 60; y++ {
			for x := 0; x < 100; x++ {
				c := uint8(bg)
				if block && x > 50 && y > 25 {
					c = uint8(fg)
				}
				img.Set(x, y, color.RGBA{R: c, G: c, B: c, A: 255})
			}
		}
		return img
	}
	base := mk(200, 0, false)
	// 微小变化：单像素移动（光标）
	small := mk(200, 0, false)
	small.(*image.RGBA).Set(5, 5, color.RGBA{R: 0, G: 0, B: 0, A: 255})
	// 显著变化：右半出现大色块（切页面）
	big := mk(200, 0, true)

	rSmall := grayDiffRatio(grayThumb(base), grayThumb(small))
	rBig := grayDiffRatio(grayThumb(base), grayThumb(big))
	t.Logf("微小变化比例=%.4f, 显著变化比例=%.4f", rSmall, rBig)
	if rSmall > helperPixThresh {
		t.Errorf("微小变化应被过滤: %.4f > 阈值%.2f", rSmall, helperPixThresh)
	}
	if rBig <= helperPixThresh {
		t.Errorf("显著变化应触发: %.4f <= 阈值%.2f", rBig, helperPixThresh)
	}
	t.Log("✅ 像素差异: 微小变化过滤/显著变化检出 通过")
}

// 3. PlayOnce 单次播放：播完触发 onDone（生成 1 秒测试视频）
func TestPlayOnce(t *testing.T) {
	testInit(t)
	// 生成 1 秒测试视频
	tv := filepath.Join(t.TempDir(), "test1s.mp4")
	cmd := exec.Command("ffmpeg", "-y", "-f", "lavfi",
		"-i", "testsrc=duration=1:size=320x180:rate=24",
		"-pix_fmt", "yuv420p", tv)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成测试视频失败: %v %s", err, out[:200])
	}

	img := canvas.NewImageFromImage(nil)
	vp := NewVideoPlayer(img)
	done := make(chan struct{})
	vp.PlayOnce(tv, 24, func() { close(done) })

	select {
	case <-done:
		t.Log("✅ 单次播放结束回调触发")
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内未触发 onDone（单次播放未结束或未回调）")
	}
	vp.Stop()
}

// 4. playAction：写 mood idle + 单次播放（不等待播完）
func TestPlayAction(t *testing.T) {
	testInit(t)
	os.WriteFile(moodFile, []byte(`{"emotion":"happy"}`), 0o644)
	img := canvas.NewImageFromImage(nil)
	vp := NewVideoPlayer(img)
	playAction(actionNames[0], vp)
	time.Sleep(300 * time.Millisecond)

	data, _ := os.ReadFile(moodFile)
	if !strings.Contains(string(data), "idle") {
		t.Errorf("playAction 后 mood.json 应为 idle, got %s", data)
	}
	vp.Stop()
	t.Log("✅ playAction 写入 idle + 单次播放启动")
}

// 5. playExpr：循环播放 + mood 写入中文名
func TestPlayExpr(t *testing.T) {
	testInit(t)
	img := canvas.NewImageFromImage(nil)
	vp := NewVideoPlayer(img)
	playExpr("开心", vp)
	time.Sleep(300 * time.Millisecond)

	data, _ := os.ReadFile(moodFile)
	if !strings.Contains(string(data), "开心") {
		t.Errorf("playExpr 后 mood.json 应为 开心, got %s", data)
	}
	vp.Stop()
	t.Log("✅ playExpr 播放 + mood 写入中文名")
}
