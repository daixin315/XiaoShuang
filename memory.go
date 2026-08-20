package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ===== 小双的长期记忆（Hermes 式：全量注入 + 上限淘汰）=====

// MemoryEntry 一条记忆
type MemoryEntry struct {
	Text string `json:"text"`
	Cat  string `json:"cat"` // user=关于用户 / note=小双的笔记
	TS   int64  `json:"ts"`
}

var (
	memoryPath string
	memMu      sync.Mutex
	memories   []MemoryEntry
)

const (
	memMaxEntries = 40   // 最多条目数
	memMaxChars   = 3000 // 最多总字符数（注入上下文预算）
)

// loadMemory 读取 memory.json
func loadMemory(exeDir string) {
	memMu.Lock()
	defer memMu.Unlock()
	memoryPath = filepath.Join(exeDir, "memory.json")
	memories = nil
	data, err := os.ReadFile(memoryPath)
	if err != nil {
		return
	}
	json.Unmarshal(data, &memories)
}

// saveMemory 写回 memory.json
func saveMemory() {
	memMu.Lock()
	defer memMu.Unlock()
	writeMemoryFile()
}

// writeMemoryFile 实际写盘（调用方必须已持有 memMu，避免死锁）
func writeMemoryFile() {
	data, _ := json.MarshalIndent(memories, "", "  ")
	os.WriteFile(memoryPath, data, 0o644)
}

// addMemory 添加记忆（去重 + 上限淘汰：条数超限删最旧，字符超限从旧往新丢）
func addMemory(text, cat string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	memMu.Lock()
	defer memMu.Unlock()
	for _, m := range memories {
		if m.Text == text {
			return // 完全重复跳过
		}
	}
	memories = append(memories, MemoryEntry{Text: text, Cat: cat, TS: time.Now().Unix()})
	// 条数上限：从最旧删
	for len(memories) > memMaxEntries {
		memories = memories[1:]
	}
	// 字符上限：从最新往旧保留，超出丢弃最旧的
	total := 0
	keep := make([]MemoryEntry, 0, len(memories))
	for i := len(memories) - 1; i >= 0; i-- {
		if total+len(memories[i].Text) > memMaxChars {
			continue
		}
		keep = append(keep, memories[i])
		total += len(memories[i].Text)
	}
	// 反转回时间正序（旧→新）
	for i, j := 0, len(keep)-1; i < j; i, j = i+1, j-1 {
		keep[i], keep[j] = keep[j], keep[i]
	}
	memories = keep
	writeMemoryFile() // 已持锁，直接写盘
}

// removeMemory 删除包含关键词的记忆，返回删除条数
func removeMemory(keyword string) int {
	memMu.Lock()
	defer memMu.Unlock()
	kw := strings.TrimSpace(keyword)
	if kw == "" {
		return 0
	}
	n := 0
	keep := memories[:0]
	for _, m := range memories {
		if strings.Contains(m.Text, kw) {
			n++
			continue
		}
		keep = append(keep, m)
	}
	memories = keep
	if n > 0 {
		writeMemoryFile() // 已持锁，直接写盘
	}
	return n
}

// memoryPrompt 生成注入 system prompt 的记忆文本（无记忆返回空串）
func memoryPrompt() string {
	memMu.Lock()
	defer memMu.Unlock()
	if len(memories) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("以下是你的长期记忆（关于用户和你的事实，聊天时自然运用；除非用户问起，否则不要主动展示这份清单）：\n")
	for _, m := range memories {
		sb.WriteString("- " + m.Text + "\n")
	}
	return strings.TrimSpace(sb.String())
}

// listMemory 生成可读的记忆清单（/记忆 命令用）
func listMemory() string {
	memMu.Lock()
	defer memMu.Unlock()
	if len(memories) == 0 {
		return "📝 我还没有记住什么～"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📝 记忆清单（%d条）：\n", len(memories)))
	for i, m := range memories {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, m.Text))
	}
	return strings.TrimSpace(sb.String())
}

// summarizeForMemory 对话后自动提取值得长期记住的新事实（后台静默，失败不打扰）
// 复用 chatWithAIEx(false,false)：不带人设和记忆注入（已有记忆已写进 prompt 做去重）
func summarizeForMemory(history []ChatMessage) {
	settingsMu.RLock()
	key := settings.APIKey
	settingsMu.RUnlock()
	if key == "" {
		return
	}

	mem := memoryPrompt()
	if mem == "" {
		mem = "（暂无）"
	}
	h := history
	if len(h) > 8 {
		h = h[len(h)-8:] // 只看最近 8 条
	}
	var sb strings.Builder
	sb.WriteString("已有记忆：\n" + mem + "\n\n刚才的对话：\n")
	for _, m := range h {
		sb.WriteString(m.Role + ": " + m.Content + "\n")
	}
	sb.WriteString("\n提取这段对话中值得长期记住的新信息（用户的重要个人信息、偏好、习惯、约定、重要事件）。要求：最多2条，每条不超过40字，不要重复已有记忆，没有新信息就只输出\"无\"。每行一条，不要编号。")

	msgs := []ChatMessage{
		{Role: "system", Content: "你是记忆提取器，只输出记忆条目或\"无\"。"},
		{Role: "user", Content: sb.String()},
	}
	reply, err := chatWithAIEx(msgs, false, false)
	if err != nil {
		return // 静默失败
	}
	for _, line := range strings.Split(reply, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "•"))
		line = strings.TrimSpace(strings.TrimPrefix(line, "1."))
		line = strings.TrimSpace(strings.TrimPrefix(line, "2."))
		if line == "" || strings.Contains(line, "无") || len(line) > 60 {
			continue
		}
		fmt.Printf("[memory] 自动记住: %s\n", line)
		addMemory(line, "user")
	}
}
