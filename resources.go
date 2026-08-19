package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ============================================================
// 资源索引：启动时扫描 assets/ 中文目录结构
//   assets/主图/main.png      主图
//   assets/表情/<中文名>.mp4   表情动画（循环播放）
//   assets/动作/<中文名>.mp4   动作动画（单次播放，播完回主图）
// ============================================================

var (
	mainImgPath string // 主图完整路径
	exprFiles   = map[string]string{} // 表情：中文名 → 路径
	actionFiles = map[string]string{} // 动作：中文名 → 路径
	exprNames   = []string{}          // 表情中文名（排序）
	actionNames = []string{}          // 动作中文名（排序）
)

// 英文情绪 → 中文表情名（mood.json 兼容映射，外部用英文写入）
var emotionAlias = map[string]string{
	"happy":      "开心",
	"smile":      "微笑",
	"daze":       "发呆",
	"sad":        "伤心",
	"think":      "思考",
	"trance":     "恍惚",
	"surprised":  "惊讶",
	"proud":      "得意",
	"shy":        "害羞",
	"sleepy":     "困倦",
	"angry":      "生气",
	"excited":    "兴奋",
	"crying":     "伤心",
	"wink":       "眨眼",
	"wronged":    "委屈",
	"cute":       "可爱",
	"scared":     "害怕",
	"speechless": "无语",
}

// scanResources 扫描素材目录（videoDir 下），构建索引；失败时打印警告不退出
func scanResources() {
	exprDir := filepath.Join(videoDir, "表情")
	actDir := filepath.Join(videoDir, "动作")
	mainDir := filepath.Join(videoDir, "主图")

	mainImgPath = filepath.Join(mainDir, "main.png")
	if !fileExists(mainImgPath) {
		fmt.Println("⚠️  主图不存在:", mainImgPath)
		mainImgPath = ""
	}

	exprFiles = map[string]string{}
	actionFiles = map[string]string{}
	exprNames = nil
	actionNames = nil

	for _, mp4 := range listMP4(exprDir) {
		name := strings.TrimSuffix(filepath.Base(mp4), ".mp4")
		exprFiles[name] = mp4
		exprNames = append(exprNames, name)
	}
	for _, mp4 := range listMP4(actDir) {
		name := strings.TrimSuffix(filepath.Base(mp4), ".mp4")
		actionFiles[name] = mp4
		actionNames = append(actionNames, name)
	}
	sort.Strings(exprNames)
	sort.Strings(actionNames)

	fmt.Printf("📁 素材索引: 主图=%s 表情=%d 动作=%d\n",
		mainImgPath, len(exprNames), len(actionNames))
}

// listMP4 列出目录下所有 .mp4（忽略大小写后缀）
func listMP4(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Println("⚠️  素材目录不存在:", dir)
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(filepath.Ext(e.Name()), ".mp4") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// resolveEmotion 情绪名（英文或中文）→ 视频路径；找不到返回主图路径
func resolveEmotion(emotion string) string {
	if v, ok := exprFiles[emotion]; ok {
		return v
	}
	if zh, ok := emotionAlias[emotion]; ok {
		if v, ok := exprFiles[zh]; ok {
			return v
		}
	}
	return mainImgPath
}

// playMainStatic 回主图（线程安全，内部用 fyne.Do 更新 UI；player 不能为 nil）
func playMainStatic(player *VideoPlayer) {
	if player == nil || mainImgPath == "" {
		return
	}
	player.Stop()
	img := loadImageFile(mainImgPath)
	if img == nil {
		return
	}
	fyneDo(func() {
		if globalVideoImg != nil {
			globalVideoImg.Image = img
			globalVideoImg.Refresh()
		}
	})
}
