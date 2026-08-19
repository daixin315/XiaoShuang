package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// runCmd 执行命令并返回 stdout
func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, buf.String()[:200])
	}
	return buf.String(), nil
}

// ChatMessage 对话消息
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatWithAI 调用 OpenAI 兼容 API 获取回复
func chatWithAI(history []ChatMessage) (string, error) {
	settingsMu.RLock()
	s := settings
	settingsMu.RUnlock()

	if s.BaseURL == "" || s.Model == "" {
		return "", fmt.Errorf("未配置 API（请在设置里填写 base_url / api_key / model）")
	}

	messages := []ChatMessage{}
	if s.System != "" {
		messages = append(messages, ChatMessage{Role: "system", Content: s.System})
	}
	messages = append(messages, history...)

	url := strings.TrimRight(s.BaseURL, "/") + "/chat/completions"
	payload := map[string]any{
		"model":       s.Model,
		"messages":    messages,
		"temperature": 0.8,
		"max_tokens":  500,
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("API %d: %s", resp.StatusCode, string(data)[:300])
	}

	var out struct {
		Choices []struct {
			Message ChatMessage `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("API 无回复")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}

// sttLocal 本地 whisper 识别（返回文字）
func sttLocal(audioPath string) (string, error) {
	settingsMu.RLock()
	s := settings
	settingsMu.RUnlock()
	cmd := strings.Fields(s.STTCmd)
	if len(cmd) == 0 {
		return "", fmt.Errorf("未配置 STT 命令")
	}
	args := append(cmd[1:], audioPath, s.STTModel)
	out, err := runCmd(cmd[0], args...)
	if err != nil {
		return "", fmt.Errorf("STT 失败: %v", err)
	}
	return strings.TrimSpace(out), nil
}

// ttsEdge Edge TTS 合成（输出 mp3 路径）
func ttsEdge(text, outFile string) error {
	settingsMu.RLock()
	s := settings
	settingsMu.RUnlock()
	bin := s.TTSBin
	if bin == "" {
		bin = "edge-tts"
	}
	_, err := runCmd(bin, "--voice", s.TTSVoice, "--text", text, "--write-media", outFile)
	return err
}
