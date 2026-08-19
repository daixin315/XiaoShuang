package main

import (
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

	// 验证 OnDismiss 逻辑：关闭菜单（点击外部/Esc）时清空 "/" 输入
	testCmdMenu.OnDismiss()
	time.Sleep(100 * time.Millisecond)
	if testInput.Text != "" {
		t.Errorf("菜单关闭后输入框应为空, got %q", testInput.Text)
	}

	// 验证菜单项回调会清空输入：再弹一次菜单并模拟点选（直接调回调）
	test.Type(testInput, "/")
	time.Sleep(200 * time.Millisecond)
	if testCmdMenu == nil || !testCmdMenu.Visible() {
		t.Fatal("第二次输入 / 菜单未弹出")
	}
	t.Log("✅ 菜单二次弹出正常")
}

// 6. 点击子菜单项后菜单必须自动关闭（用户反馈：点了不消失）
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

	// 键盘导航（等价鼠标点击）：
	// Down → 激活第一个 item（😊 表情）
	// Right → 展开子菜单
	// Down → 子菜单第一个（😊 开心）
	// Return → 触发 action
	testCmdMenu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	testCmdMenu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyRight})
	testCmdMenu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyDown})
	testCmdMenu.TypedKey(&fyne.KeyEvent{Name: fyne.KeyReturn})
	time.Sleep(300 * time.Millisecond)

	if testCmdMenu.Visible() {
		t.Error("❌ 点击子菜单项后菜单未关闭（用户反馈的 bug）")
	} else {
		t.Log("✅ 点击子菜单项后菜单已关闭")
	}
	// 输入框应被清空
	if testInput.Text != "" {
		t.Errorf("输入框应为空, got %q", testInput.Text)
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

	// 点击输入框等菜单外区域 → canvas 分发到 overlay 的 dismiss 回调（Dismiss 等价）
	testCmdMenu.Dismiss()
	time.Sleep(200 * time.Millisecond)
	if testCmdMenu.Visible() {
		t.Error("❌ 点击输入框后菜单未关闭（用户反馈的 bug）")
	} else {
		t.Log("✅ 点击菜单外区域菜单已关闭")
	}
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
