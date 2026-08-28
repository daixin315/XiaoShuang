package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ===== 语音唤醒（功能1）=====
// 后台监听：每 4 秒录 3 秒音频 → whisper tiny 识别 → 含"小双"→ 聆听模式（录 5 秒 → medium 识别 → 聊天）
// 与按住说话录音互斥（recCmd 非空时跳过）

// startWakeLoop 启动唤醒监听（goroutine；Windows 无 pulse 音频，跳过）
func startWakeLoop() {
	if isWindows() {
		fmt.Println("[wake] Windows 暂不支持语音唤醒（无 pulse）")
		return
	}
	go func() {
		fmt.Println("[wake] 唤醒监听已启动（喊“小双”试试）")
		for {
			time.Sleep(4 * time.Second)
			// 忙时跳过（按住说话已删除，无需录音互斥）
			if isTaskBusy() {
				continue
			}
			// 录 3 秒 → tiny 识别
			f := filepath.Join(os.TempDir(), fmt.Sprintf("wake_%d.wav", time.Now().UnixNano()))
			if exec.Command("ffmpeg", "-y", "-loglevel", "error", "-f", "pulse", "-i", "default", "-t", "3", f).Run() != nil {
				os.Remove(f)
				continue
			}
			text := wakeStt(f)
			os.Remove(f)
			if strings.Contains(text, "小双") {
				fmt.Println("[wake] 唤醒词命中！进入聆听模式")
				wakeListen()
			}
		}
	}()
}

// wakeStt 用 tiny 模型快速识别（唤醒监听用，快）
func wakeStt(audio string) string {
	settingsMu.RLock()
	cmdline := settings.STTCmd
	settingsMu.RUnlock()
	fields := strings.Fields(cmdline)
	if len(fields) == 0 {
		return ""
	}
	// python3 scripts/whisper_stt.py <audio> tiny（绝对路径，systemd 下 cwd 不是项目目录）
	script := fields[1]
	if strings.HasPrefix(script, "scripts/") {
		script = filepath.Join(exeDirOf(), script)
	}
	args := append([]string{script}, audio, "tiny")
	out, err := runCmd(fields[0], args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// wakeListen 唤醒后：提示音 → 录 5 秒 → medium 识别 → 走聊天
func wakeListen() {
	// 提示"我在呢"
	go func() {
		speakMu.Lock()
		defer speakMu.Unlock()
		mp3 := filepath.Join(os.TempDir(), fmt.Sprintf("wake_ok_%d.mp3", time.Now().UnixNano()))
		if err := ttsEdge("我在呢，请说", mp3); err == nil {
			playAudio(mp3)
		}
	}()

	f := filepath.Join(os.TempDir(), fmt.Sprintf("listen_%d.wav", time.Now().UnixNano()))
	if exec.Command("ffmpeg", "-y", "-loglevel", "error", "-f", "pulse", "-i", "default", "-t", "5", f).Run() != nil {
		os.Remove(f)
		return
	}
	text, err := sttLocal(f)
	os.Remove(f)
	if err != nil || strings.TrimSpace(text) == "" {
		globalAddMsg("assistant", "🤔 没听清，再说一次？")
		return
	}
	fmt.Printf("[wake] 听到: %s\n", text)
	// 走正常聊天链路（窗口输入等价）
	if globalAddMsg != nil {
		globalAddMsg("user", "🎤 "+text)
	}
	sendChat(text, globalAddMsg, globalPlayer)
}
