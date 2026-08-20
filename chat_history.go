package main

import (
	"os"
	"path/filepath"
	"strings"
)

// ===== 对话历史持久化（textCache 模式：内存 []ChatMessage + 分隔符文本落盘）=====
// 文件 chat_history.txt 每行: role︿内容（行序=时间序，重启后小双记得上次聊到哪）

const (
	chatHistFile = "chat_history.txt"
	chatHistMax  = 200 // 持久化保留最近条数（防无限增长）
	chatAICtxMax = 50  // 每次 AI 请求携带的上下文条数（控制 token）
	chatHistSep  = "︿" // 分隔符（与记忆系统一致，罕见字符）
)

var chatHistPath string

// loadChatHistory 启动时读对话历史
func loadChatHistory(exeDir string) {
	chatHistPath = filepath.Join(exeDir, chatHistFile)
	chatLogMu.Lock()
	defer chatLogMu.Unlock()
	chatHistory = nil
	data, err := os.ReadFile(chatHistPath)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, chatHistSep, 2)
		if len(parts) != 2 {
			continue
		}
		role := strings.TrimSpace(parts[0])
		content := strings.TrimSpace(parts[1])
		if (role == "user" || role == "assistant") && content != "" {
			chatHistory = append(chatHistory, ChatMessage{Role: role, Content: content})
		}
	}
	if len(chatHistory) > 0 {
		chatHistPath = filepath.Join(exeDir, chatHistFile)
	}
}

// appendChat 记录一条消息（内存 + 落盘 + 超200条截断最旧）
func appendChat(role, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	chatLogMu.Lock()
	chatHistory = append(chatHistory, ChatMessage{Role: role, Content: content})
	for len(chatHistory) > chatHistMax {
		chatHistory = chatHistory[1:]
	}
	saveChatHistoryLocked()
	chatLogMu.Unlock()
}

// saveChatHistoryLocked 写盘（调用方必须已持有 chatLogMu）
func saveChatHistoryLocked() {
	if chatHistPath == "" {
		return
	}
	var sb strings.Builder
	for _, m := range chatHistory {
		// 内容规范化：单行 + 分隔符替换（防破坏格式）
		c := strings.ReplaceAll(m.Content, "\n", " ")
		c = strings.ReplaceAll(c, "\r", " ")
		c = strings.ReplaceAll(c, chatHistSep, "丨")
		sb.WriteString(m.Role)
		sb.WriteString(chatHistSep)
		sb.WriteString(c)
		sb.WriteString("\n")
	}
	os.WriteFile(chatHistPath, []byte(sb.String()), 0o644)
}

// recentHistory 取最近 N 条对话（AI 请求用，控制 token）
func recentHistory(n int) []ChatMessage {
	chatLogMu.Lock()
	defer chatLogMu.Unlock()
	if len(chatHistory) <= n {
		return append([]ChatMessage{}, chatHistory...)
	}
	return append([]ChatMessage{}, chatHistory[len(chatHistory)-n:]...)
}
