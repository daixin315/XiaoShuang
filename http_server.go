package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ===== 小双 HTTP 命令接口 =====
//   POST http://127.0.0.1:8721/exec  {"cmd":"ls -la"}
//     规则层黑名单 → AI 安全判断 → 执行（30s 超时，输出截断 8KB）
//   GET  http://127.0.0.1:8721/status  小双状态
// 只绑定 127.0.0.1（本地回环），外部网络不可达

const httpAddr = "127.0.0.1:8721"

// dangerousPatterns 规则层黑名单（命中直接拒绝，不经 AI）
var dangerousPatterns = []string{
	"rm -rf /*", "rm -fr /*", "rm -rf /bin", "rm -rf /etc", "rm -rf /usr",
	"rm -rf /var", "rm -rf /opt", "rm -rf /root", "rm -rf /sbin", "rm -rf /lib",
	"mkfs", "mkfs.ext", "mkfs.xfs",
	"dd if=/dev/zero", "dd of=/dev/sd", "shutdown", "reboot", "poweroff",
	"init 0", "init 6", "> /dev/sda", "> /dev/sdb",
	"chmod -R 777 /", "chown -R /", ":(){", "fork bomb",
	"curl -s.*|.*bash", "curl -s.*|.*sh ", "wget -q.*|.*bash", "wget .*|.*sh",
	"git clone.*&&.*rm", "mv / /", "usermod", "passwd", "useradd", "deluser",
	"iptables -F", "ufw disable", "systemctl stop", "kill -9 1",
}

// isRMRoot 精确判断 rm -rf 根目录/系统路径（rm -rf /tmp/xxx 这类不拦，交 AI 判断）
func isRMRoot(cmd string) bool {
	lower := strings.ToLower(strings.TrimSpace(cmd))
	for _, p := range []string{
		"rm -rf / ", "rm -fr / ", "rm -rf /*", "rm -rf /.", "rm -rf /..",
		"rm -rf ~", "rm -rf *", "rm -rf .*",
	} {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// checkDangerPatterns 规则层检查，命中返回匹配的模式
func checkDangerPatterns(cmd string) string {
	lower := strings.ToLower(cmd)
	for _, p := range dangerousPatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return p
		}
	}
	return ""
}

// aiCheckDanger AI 判断命令安全性；返回 (安全?, 原因)
// AI 不可用/超时 → 保守拒绝（安全优先）
// sudo/高权限命令用更严格标准（AI 会看到 sudo 前缀并重点审查）
func aiCheckDanger(cmd string) (bool, string) {
	msgs := []ChatMessage{
		{Role: "system", Content: "你是 Linux 命令安全审查员。判断命令是否安全：只读/查询/无害操作=安全；删除数据/修改系统配置/下载执行/网络攻击/高危操作=危险。命令含 sudo（root 权限）时审查标准更严格：普通文件操作/安装软件/查看信息=安全；改系统关键配置/删系统文件/改权限/动分区=危险。只输出一行：安全: 原因 或 危险: 原因"},
		{Role: "user", Content: "命令：" + cmd},
	}
	done := make(chan struct{})
	var reply string
	var err error
	go func() {
		reply, err = chatWithAIEx(msgs, false, false)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		return false, "AI 审查超时，保守拒绝"
	}
	if err != nil {
		return false, "AI 审查失败（" + err.Error() + "），保守拒绝"
	}
	// 按开头判断（prompt 要求输出 "安全: 原因" 或 "危险: 原因"）
	// 不能用 Contains("危险")——AI 会说"无任何危险操作"导致误杀
	lower := strings.ToLower(strings.TrimSpace(reply))
	if strings.HasPrefix(lower, "危险") || strings.HasPrefix(lower, "不安全") {
		return false, strings.TrimSpace(reply)
	}
	return true, strings.TrimSpace(reply)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// handleExec POST /exec（安全审查+执行排队串行，handler 等结果）
func handleExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Cmd string `json:"cmd"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	cmd := strings.TrimSpace(req.Cmd)
	if cmd == "" {
		http.Error(w, "empty cmd", http.StatusBadRequest)
		return
	}
	fmt.Printf("[http] 收到命令: %s（排队等待）\n", cmd)

	type execResult struct {
		dangerous bool
		errorMsg  string
		output    string
	}
	done := make(chan execResult, 1)
	enqueue(func() {
		// 1. 规则层黑名单
		if p := checkDangerPatterns(cmd); p != "" {
			fmt.Printf("[http] 规则层拦截: %s\n", p)
			done <- execResult{dangerous: true, errorMsg: "命令命中危险模式: " + p}
			return
		}
		if isRMRoot(cmd) {
			fmt.Printf("[http] 规则层拦截: rm 根目录\n")
			done <- execResult{dangerous: true, errorMsg: "命令命中危险模式: rm 根目录/系统路径"}
			return
		}
		// 2. AI 安全判断
		safe, reason := aiCheckDanger(cmd)
		if !safe {
			fmt.Printf("[http] AI 判定危险: %s\n", reason)
			done <- execResult{dangerous: true, errorMsg: "AI 判定危险: " + reason}
			return
		}
		// 3. 执行（30s 超时，输出截断 8KB）
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		execCmd := cmd
		if strings.Contains(cmd, "sudo") {
			// sudo 自动注入密码（sudo_pass.txt 非空时），避免卡密码输入
			if sp := readSudoPass(); sp != "" {
				execCmd = fmt.Sprintf("echo %s | sudo -S %s 2>/dev/null", shellQuote(sp), cmd)
			}
		}
		out, err := exec.CommandContext(ctx, "/bin/bash", "-c", execCmd).CombinedOutput()
		output := string(out)
		if len(output) > 8000 {
			output = output[:8000] + "\n...(输出截断)"
		}
		if err != nil {
			done <- execResult{errorMsg: err.Error(), output: output}
			return
		}
		fmt.Printf("[http] 执行完成 (%d bytes)\n", len(output))
		done <- execResult{output: output}
	})

	res := <-done // 等队列任务完成（串行保证一次一个命令）
	switch {
	case res.dangerous:
		writeJSON(w, map[string]any{"ok": false, "dangerous": true, "error": res.errorMsg})
	case res.errorMsg != "":
		writeJSON(w, map[string]any{"ok": false, "output": res.output, "error": res.errorMsg})
	default:
		writeJSON(w, map[string]any{"ok": true, "output": res.output})
	}
}

// handleChat POST /chat  {"text":"消息内容"} —— 外部(Hermes)代用户转达消息
// 复用主对话链路：记录历史+窗口气泡+表情+5分钟总结；回复通过 HTTP 返回
func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		http.Error(w, "empty text", http.StatusBadRequest)
		return
	}
	fmt.Printf("[http] 收到转达消息: %s\n", text)

	// 记忆命令直接处理（不需 AI）
	if reply, handled := handleMemoryCommand(text); handled {
		writeJSON(w, map[string]any{"ok": true, "reply": reply})
		return
	}
	maybeTick() // 时间流转（与窗口输入一致）
	// 待办命令
	if reply, handled := handleTodoCommand(text); handled {
		writeJSON(w, map[string]any{"ok": true, "reply": reply})
		return
	}
	// 触发词表演（秒响应，不走 AI）
	if act := matchTrigger(text); act != "" {
		if _, ok := actionFiles[act]; ok {
			playAction(act, globalPlayer)
		} else {
			playExpr(act, globalPlayer)
		}
		writeJSON(w, map[string]any{"ok": true, "reply": "🎭 来啦～"})
		return
	}
	// 情绪识别
	applyMoodReaction(detectMood(text), globalPlayer)
	// 忙时提示
	if isTaskBusy() {
		writeJSON(w, map[string]any{"ok": false, "busy": true, "reply": "🫥 小双正在忙，等我一下下～"})
		return
	}
	// 记录用户消息（窗口同步显示 + 持久化）
	appendChat("user", text)
	globalAddMsg("user", text)
	setMoodNow("think", globalPlayer)
	scheduleSummarize() // 转达也算对话活跃

	done := make(chan string, 1)
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
			playExtractedAction(actName, globalPlayer)
		}
		appendChat("assistant", cleanReply)
		globalAddMsg("assistant", cleanReply)
		scheduleSummarize() // 回复完成 → 立即总结本轮对话进记忆（与窗口输入完全一致）
		if isErr {
			setMoodNow("sad", globalPlayer)
		} else {
			setMoodNow("happy", globalPlayer)
		}
		done <- cleanReply
	})
	reply := <-done
	writeJSON(w, map[string]any{"ok": true, "reply": reply})
}

// handleAction POST /action  {"name":"单人荡秋千"} —— 远程让小双表演动作(单次)或表情(循环)
func handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "empty name", http.StatusBadRequest)
		return
	}
	if globalPlayer == nil {
		http.Error(w, "player not ready", http.StatusServiceUnavailable)
		return
	}
	if _, ok := actionFiles[name]; ok {
		fmt.Printf("[http] 播放动作: %s\n", name)
		playAction(name, globalPlayer) // 单次播放，播完回主图
		writeJSON(w, map[string]any{"ok": true, "played": "action", "name": name})
		return
	}
	if _, ok := exprFiles[name]; ok {
		fmt.Printf("[http] 播放表情: %s\n", name)
		playExpr(name, globalPlayer) // 循环播放
		writeJSON(w, map[string]any{"ok": true, "played": "expr", "name": name})
		return
	}
	http.Error(w, "no such action/expr: "+name, http.StatusNotFound)
}

// handleMood POST /mood  {"emotion":"think"} —— 外部(Hermes)设置情绪，小双播放对应表情
// emotion 支持中文(思考/开心)或英文(think/happy)；配合 /exec 实现"我干活她思考"
func handleMood(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Emotion string `json:"emotion"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	emotion := strings.TrimSpace(req.Emotion)
	if _, ok := exprFiles[emotion]; !ok {
		if zh, ok := emotionAlias[emotion]; ok {
			emotion = zh // 英文别名 → 中文表情
		}
	}
	if _, ok := exprFiles[emotion]; !ok {
		http.Error(w, "unknown emotion: "+req.Emotion, http.StatusBadRequest)
		return
	}
	if globalPlayer == nil {
		http.Error(w, "player not ready", http.StatusServiceUnavailable)
		return
	}
	fmt.Printf("[http] 设置情绪: %s\n", emotion)
	playExpr(emotion, globalPlayer) // 循环播放对应表情
	writeJSON(w, map[string]any{"ok": true, "emotion": emotion})
}

// speakMu 语音播报互斥（一次只播一条）
var speakMu sync.Mutex

// handleSpeak POST /speak  {"text":"正在分析数据..."} —— 小双语音播报（Hermes 干活进度）
// 气泡显示 + Edge TTS 语音播放（后台，不阻塞响应）
func handleSpeak(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		http.Error(w, "empty text", http.StatusBadRequest)
		return
	}
	fmt.Printf("[http] 语音播报: %s\n", text)
	if globalAddMsg != nil {
		globalAddMsg("assistant", "🔊 "+text)
	}
	go func() {
		speakMu.Lock()
		defer speakMu.Unlock()
		mp3 := filepath.Join(os.TempDir(), fmt.Sprintf("speak_%d.mp3", time.Now().UnixNano()))
		if err := ttsEdge(text, mp3); err == nil {
			playAudio(mp3)
		}
	}()
	writeJSON(w, map[string]any{"ok": true})
}

// shellQuote 单引号包裹（shell 安全引用，防密码含特殊字符）
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// handleStatus GET /status
func handleStatus(w http.ResponseWriter, r *http.Request) {
	emotion := readMood()
	writeJSON(w, map[string]any{
		"name":    "小双",
		"alive":   true,
		"emotion": emotion,
		"memory":  memoryStats(),
	})
}

// memoryStats 记忆统计（status 接口用）
func memoryStats() map[string]int {
	memMu.Lock()
	defer memMu.Unlock()
	return map[string]int{
		"core":      len(coreLines),
		"system":    len(sysLines),
		"important": len(impLines),
		"timeline":  len(timeline),
	}
}

func truncateRune(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// startHTTPServer 启动本地命令接口（goroutine 运行）
func startHTTPServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/exec", handleExec)
	mux.HandleFunc("/chat", handleChat)
	mux.HandleFunc("/action", handleAction)
	mux.HandleFunc("/mood", handleMood)
	mux.HandleFunc("/speak", handleSpeak)
	mux.HandleFunc("/status", handleStatus)
	srv := &http.Server{Addr: httpAddr, Handler: mux}
	fmt.Printf("[http] 接口已启动: http://%s/exec /chat /action /mood /speak /status\n", httpAddr)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Printf("[http] 服务退出: %v\n", err)
	}
}
