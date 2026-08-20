package main

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// ===== 空闲随机小动作（功能3）=====
// 小双空闲（没在干活）时每 4-8 分钟随机表演一个动作，播完回主图，更像活物

// startIdleActions 启动空闲动作循环
func startIdleActions() {
	go func() {
		for {
			time.Sleep(4*time.Minute + time.Duration(rand.Intn(4))*time.Minute)
			if isTaskBusy() || globalPlayer == nil || len(actionNames) == 0 {
				continue // 忙或资源未就绪就跳过
			}
			name := actionNames[rand.Intn(len(actionNames))]
			fmt.Printf("[idle] 空闲表演动作: %s\n", name)
			playAction(name, globalPlayer) // 单次播放，播完自动回主图
		}
	}()
}

// ===== 回忆功能（功能7）=====
// 每 3-5 小时小双主动从重要记忆里挑一条，AI 润色成一句回忆，窗口气泡显示

// startRecallTimer 启动定时回忆
func startRecallTimer() {
	go func() {
		// 启动后先等 30 分钟（别一开机就说话）
		time.Sleep(30 * time.Minute)
		for {
			time.Sleep(3*time.Hour + time.Duration(rand.Intn(2))*time.Hour)
			if isTaskBusy() || globalPlayer == nil {
				continue
			}
			recallOnce()
		}
	}()
}

// recallOnce 挑一条重要记忆 → AI 润色成回忆话 → 气泡显示
func recallOnce() {
	memMu.Lock()
	if len(impLines) == 0 {
		memMu.Unlock()
		return
	}
	entry := impLines[rand.Intn(len(impLines))]
	memMu.Unlock()

	msgs := []ChatMessage{
		{Role: "system", Content: "你是小双。根据下面这条记忆，说一句回忆的话（像对老朋友提起往事，自然温暖，不超过30字，不要解释这条记忆）。"},
		{Role: "user", Content: "记忆：" + entry},
	}
	enqueue(func() {
		reply, err := chatWithAIEx(msgs, true, false) // 带人设、不注入记忆
		if err != nil || strings.TrimSpace(reply) == "" {
			return
		}
		clean, _ := extractAction(reply) // 万一 AI 加了表演标记
		globalAddMsg("assistant", "💭 "+clean)
		setMoodNow("happy", globalPlayer)
		fmt.Printf("[recall] 小双回忆: %s\n", entry)
	})
}

// ===== 触发词表演（功能4）=====
// 消息包含表情/动作名或别名 → 本地直接播放（秒响应，不走 AI）

// triggerAliases 常用别名 → 实际表演名
// 注意：情绪类词（开心/难过/哭/累等）不放这里，交给 detectMood 情绪识别（AI 会安慰）
var triggerAliases = map[string]string{
	"拜拜": "眨眼", "再见": "眨眼", "晚安": "困倦",
	"秋千": "单人荡秋千", "荡秋千": "单人荡秋千", "摇": "单人荡秋千",
	"跳舞": "蝴蝶围圈", "蝴蝶": "蝴蝶围圈", "飞": "蝴蝶围圈",
	"锦鲤": "河边锦鲤", "鱼": "河边锦鲤", "摸鱼": "河边锦鲤",
	"戏水": "河边戏水", "水": "河边戏水",
	"马": "看马摸马", "骑马": "看马摸马",
	"花": "躺花丛", "采花": "摘野果喂狗", "野果": "摘野果喂狗", "狗": "摘野果喂狗",
	"凤凰": "凤凰落手臂",
}

// matchTrigger 匹配触发词，返回要表演的名字（找不到返回空串）
// 注意：表情名不做完整名匹配——情绪词（开心/难过等）走 detectMood，避免误触发
func matchTrigger(text string) string {
	for alias, name := range triggerAliases {
		if strings.Contains(text, alias) {
			return name
		}
	}
	// 完整名匹配（仅动作，表情归情绪识别）
	for _, n := range actionNames {
		if strings.Contains(text, n) {
			return n
		}
	}
	return ""
}
