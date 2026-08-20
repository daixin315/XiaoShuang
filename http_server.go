package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
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
	if strings.Contains(strings.ToLower(reply), "危险") {
		return false, strings.TrimSpace(reply)
	}
	return true, strings.TrimSpace(reply)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// handleExec POST /exec
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
	fmt.Printf("[http] 收到命令: %s\n", cmd)

	// 1. 规则层黑名单
	if p := checkDangerPatterns(cmd); p != "" {
		fmt.Printf("[http] 规则层拦截: %s\n", p)
		writeJSON(w, map[string]any{"ok": false, "dangerous": true, "error": "命令命中危险模式: " + p})
		return
	}
	if isRMRoot(cmd) {
		fmt.Printf("[http] 规则层拦截: rm 根目录\n")
		writeJSON(w, map[string]any{"ok": false, "dangerous": true, "error": "命令命中危险模式: rm 根目录/系统路径"})
		return
	}
	// 2. AI 安全判断
	safe, reason := aiCheckDanger(cmd)
	if !safe {
		fmt.Printf("[http] AI 判定危险: %s\n", reason)
		writeJSON(w, map[string]any{"ok": false, "dangerous": true, "error": "AI 判定危险: " + reason})
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
		writeJSON(w, map[string]any{"ok": false, "output": output, "error": err.Error()})
		return
	}
	fmt.Printf("[http] 执行完成 (%d bytes)\n", len(output))
	writeJSON(w, map[string]any{"ok": true, "output": output})

	// 对话区告知用户小双干了什么
	fyneDo(func() {
		if globalAddMsg != nil {
			msg := "🖥️ 刚帮主人执行了命令：`" + cmd + "`\n" + truncateRune(output, 150)
			globalAddMsg("assistant", msg)
		}
	})
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
	mux.HandleFunc("/status", handleStatus)
	srv := &http.Server{Addr: httpAddr, Handler: mux}
	fmt.Printf("[http] 命令接口已启动: http://%s/exec\n", httpAddr)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Printf("[http] 服务退出: %v\n", err)
	}
}
