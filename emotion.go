package main

import (
	"fmt"
	"strings"
)

// ===== 情绪识别（功能2）：检测用户消息情绪 → 自动换表情 =====
// 本地关键词秒响应，不走 AI；配合触发词使用（触发词优先，情绪其次）

var sadWords = []string{
	"难过", "伤心", "哭", "好烦", "烦死", "郁闷", "焦虑", "压力", "失望",
	"失败", "讨厌", "难受", "emo", "emo了", "委屈", "累死", "好累", "崩溃",
	"气死", "生气", "愤怒", "不开心", "孤独", "想哭",
}

var happyWords = []string{
	"开心", "哈哈", "哈哈哈", "太好了", "高兴", "棒", "太棒", "爽", "成功",
	"兴奋", "喜欢", "笑死", "耶", "完美", "快乐", "嘿嘿", "嘻嘻", "中奖", "搞定",
}

// detectMood 检测消息情绪，返回 "sad"/"happy"/""（sad 优先，防误判开心）
func detectMood(text string) string {
	for _, w := range sadWords {
		if strings.Contains(text, w) {
			return "sad"
		}
	}
	for _, w := range happyWords {
		if strings.Contains(text, w) {
			return "happy"
		}
	}
	return ""
}

// applyMoodReaction 按情绪播放对应表情（不阻塞，聊天继续）
func applyMoodReaction(mood string, player *VideoPlayer) {
	switch mood {
	case "sad":
		playExpr("伤心", player)
		fmt.Println("[mood] 检测到低落情绪 → 伤心表情")
	case "happy":
		playExpr("开心", player)
		fmt.Println("[mood] 检测到开心情绪 → 开心表情")
	}
}
