package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ===== 待办清单（功能3）=====
// 说"记一下周五开会"/"提醒我..." → AI 解析时间 → 存 todos.txt → 到点气泡+语音提醒
// 存储：todos.txt 每行: unix时间︿内容（与记忆系统同款分隔符文本）

type TodoItem struct {
	Time int64  `json:"time"` // unix 秒
	Text string `json:"text"`
}

var (
	todoPath string
	todoMu   sync.Mutex
	todos    []TodoItem
)

const todoFile = "todos.txt"

// loadTodos 启动时读待办
func loadTodos(exeDir string) {
	todoMu.Lock()
	defer todoMu.Unlock()
	todoPath = filepath.Join(exeDir, todoFile)
	todos = nil
	data, err := os.ReadFile(todoPath)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, memSep, 2)
		if len(parts) != 2 {
			continue
		}
		ts, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		if err != nil {
			continue
		}
		todos = append(todos, TodoItem{Time: ts, Text: strings.TrimSpace(parts[1])})
	}
}

// saveTodosLocked 写盘（调用方持锁）
func saveTodosLocked() {
	if todoPath == "" {
		return
	}
	var sb strings.Builder
	for _, t := range todos {
		sb.WriteString(strconv.FormatInt(t.Time, 10))
		sb.WriteString(memSep)
		sb.WriteString(strings.ReplaceAll(t.Text, "\n", " "))
		sb.WriteString("\n")
	}
	os.WriteFile(todoPath, []byte(sb.String()), 0o644)
}

// addTodo 添加待办
func addTodo(t time.Time, text string) {
	todoMu.Lock()
	defer todoMu.Unlock()
	todos = append(todos, TodoItem{Time: t.Unix(), Text: text})
	saveTodosLocked()
}

// listTodos 待办列表文本
func listTodos() string {
	todoMu.Lock()
	defer todoMu.Unlock()
	if len(todos) == 0 {
		return "📌 目前没有待办～"
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📌 待办（%d 条）：\n", len(todos)))
	for i, t := range todos {
		when := time.Unix(t.Time, 0).Format("01-02 15:04")
		sb.WriteString(fmt.Sprintf("%d. %s  %s\n", i+1, when, t.Text))
	}
	return strings.TrimSpace(sb.String())
}

// parseTodo AI 解析待办语句 → (提醒时间, 内容, 错误)
func parseTodo(text string) (time.Time, string, error) {
	now := time.Now()
	msgs := []ChatMessage{
		{Role: "system", Content: "你是时间解析器。从用户的待办语句中提取提醒时间和事项内容。现在是 " + now.Format("2006-01-02 15:04") +
			"。输出格式（严格一行）：YYYY-MM-DD HH:MM|内容。无法确定时间就用一个合理猜测；内容不超过20字。"},
		{Role: "user", Content: text},
	}
	reply, err := chatWithAIEx(msgs, false, false)
	if err != nil {
		return time.Time{}, "", err
	}
	reply = strings.TrimSpace(reply)
	parts := strings.SplitN(reply, "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", fmt.Errorf("解析失败: %s", reply)
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", strings.TrimSpace(parts[0]), time.Local)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("时间格式: %s", parts[0])
	}
	content := strings.TrimSpace(parts[1])
	if content == "" {
		return time.Time{}, "", fmt.Errorf("内容为空")
	}
	return t, content, nil
}

// todoKeywords 触发待办解析的关键词
var todoKeywords = []string{"记一下", "提醒我", "提醒一下", "待办", "别忘了"}

// handleTodoCommand 检测并处理待办语句，返回(回复文本, 是否已处理)
// "记一下xxx/提醒我xxx" → 异步 AI 解析后入待办；/待办 → 同步返回列表
func handleTodoCommand(text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "/待办" {
		return listTodos(), true
	}
	for _, kw := range todoKeywords {
		if strings.Contains(trimmed, kw) {
			// 异步 AI 解析（不阻塞）
			go func() {
				t, content, err := parseTodo(trimmed)
				if err != nil {
					globalAddMsg("assistant", "🤔 我没听懂时间……再说一次？格式：记一下 明天上午10点开会")
					return
				}
				addTodo(t, content)
				globalAddMsg("assistant", fmt.Sprintf("📌 记好啦：%s（%s 提醒你）", content, t.Format("01-02 15:04")))
			}()
			return "📌 收到，正在安排提醒～", true
		}
	}
	return "", false
}

// startTodoLoop 待办检查循环：每分钟扫一次，到点提醒（气泡+语音）
func startTodoLoop() {
	go func() {
		for {
			time.Sleep(30 * time.Second)
			checkTodos()
		}
	}()
}

// checkTodos 到点提醒并移除
func checkTodos() {
	now := time.Now()
	todoMu.Lock()
	var keep []TodoItem
	removed := false
	for _, t := range todos {
		if now.After(time.Unix(t.Time, 0)) {
			removed = true
			msg := "⏰ 提醒你：" + t.Text
			fmt.Printf("[todo] 提醒: %s\n", t.Text)
			// 气泡显示
			go func(m string) {
				globalAddMsg("assistant", m)
			}(msg)
			// 语音播报
			go func(txt string) {
				speakMu.Lock()
				defer speakMu.Unlock()
				mp3 := filepath.Join(os.TempDir(), fmt.Sprintf("todo_%d.mp3", time.Now().UnixNano()))
				if err := ttsEdge("主人，"+txt, mp3); err == nil {
					playAudio(mp3)
				}
			}(t.Text)
			continue // 已提醒，删除
		}
		keep = append(keep, t)
	}
	todos = keep
	if removed {
		saveTodosLocked()
	}
	todoMu.Unlock()
}
