package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Settings 侧边栏接入配置（存 exe 同目录 settings.json）
type Settings struct {
	BaseURL        string `json:"base_url"`         // OpenAI 兼容 API 地址
	APIKey         string `json:"api_key"`          // API Key
	Model          string `json:"model"`            // 对话模型
	System         string `json:"system"`           // 系统提示词（人设）
	STTMode        string `json:"stt_mode"`         // local(whisper) | openai
	STTModel       string `json:"stt_model"`        // 本地 whisper 模型（small/medium）
	STTCmd         string `json:"stt_cmd"`          // 本地 STT 调用命令（含 venv python）
	TTSMode        string `json:"tts_mode"`         // edge | openai
	TTSVoice       string `json:"tts_voice"`        // edge 音色
	TTSBin         string `json:"tts_bin"`          // edge-tts 可执行路径
	RecordDev      string `json:"record_dev"`       // 录音设备（ffmpeg 参数，已废弃保留兼容）
	HelpInterval   int    `json:"help_interval"`    // Help 桌面观察间隔（秒，默认5）
	HelpVision     string `json:"help_vision"`      // 已废弃（固定 DeepSeek Vision），保留兼容旧配置
	HelpPrompt     string `json:"help_prompt"`      // 已废弃（由 HelpOncePrompt/HelpLivePrompt 取代）
	HelpOncePrompt string `json:"help_once_prompt"` // 一次性"帮助"完整提示词（空=内置默认）
	HelpLivePrompt string `json:"help_live_prompt"` // "实时帮助"完整提示词（空=内置默认）
	VisionBaseURL  string `json:"vision_base_url"`  // 视觉模型 API 地址（空=用对话 BaseURL）
	VisionAPIKey   string `json:"vision_api_key"`   // 视觉模型 Key（空=用对话 APIKey）
	VisionModel    string `json:"vision_model"`     // 视觉模型名（空=deepseek-v4-flash-vision-exp）
}

// helperInterval 读取 Help 观察间隔（0或未配 → 默认5秒）
func helperInterval() int {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings.HelpInterval > 0 {
		return settings.HelpInterval
	}
	return helperDefaultInt
}

// helperVision 视觉方案：固定 DeepSeek Vision（OCR 选项已废弃，用户确认无用）
func helperVision() string {
	return "deepseek"
}

// helperOncePrompt 一次性"帮助"的完整提示词（设置里可改，空=内置默认）
func helperOncePrompt() string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if s := strings.TrimSpace(settings.HelpOncePrompt); s != "" {
		return s
	}
	return visionPrompt
}

// helperLivePrompt "实时帮助"的完整提示词（设置里可改，空=内置默认）
func helperLivePrompt() string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if s := strings.TrimSpace(settings.HelpLivePrompt); s != "" {
		return s
	}
	return visionPrompt
}

// readSudoPass 读 sudo 密码（独立文件 sudo_pass.txt，gitignore，避免被 saveSettings 覆盖）
func readSudoPass() string {
	data, err := os.ReadFile(filepath.Join(exeDirOf(), "sudo_pass.txt"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

var (
	settings     Settings
	settingsMu   sync.RWMutex
	settingsPath string
)

func defaultSettings() Settings {
	return Settings{
		BaseURL:   "https://api.deepseek.com/v1", // 默认 DeepSeek（用户只需填 API Key）
		APIKey:    "",
		Model:     "deepseek-v4-flash",
		System:    "你是一个温柔可爱的少女，名字叫小双，是双鱼座。回复要简短自然，语气温柔亲切，像朋友一样聊天。",
		STTMode:   "local",
		STTModel:  "medium",
		STTCmd:    "python3 scripts/whisper_stt.py", // 相对项目目录，依赖见 requirements.txt
		TTSMode:   "edge",
		TTSVoice:  "zh-CN-XiaoxiaoNeural",
		TTSBin:    "edge-tts", // PATH 中的 edge-tts
		RecordDev: "",
	}
}

func loadSettings(dir string) {
	settingsPath = filepath.Join(dir, "settings.json")
	settings = defaultSettings()
	data, err := os.ReadFile(settingsPath)
	if err == nil {
		var s Settings
		if json.Unmarshal(data, &s) == nil {
			// 合并默认值（新字段有默认）
			d := defaultSettings()
			if s.BaseURL != "" {
				d.BaseURL = s.BaseURL
			}
			if s.APIKey != "" {
				d.APIKey = s.APIKey
			}
			if s.Model != "" {
				d.Model = s.Model
			}
			if s.System != "" {
				d.System = s.System
			}
			if s.STTMode != "" {
				d.STTMode = s.STTMode
			}
			if s.STTModel != "" {
				d.STTModel = s.STTModel
			}
			if s.STTCmd != "" {
				d.STTCmd = s.STTCmd
			}
			if s.TTSMode != "" {
				d.TTSMode = s.TTSMode
			}
			if s.TTSVoice != "" {
				d.TTSVoice = s.TTSVoice
			}
			if s.TTSBin != "" {
				d.TTSBin = s.TTSBin
			}
			if s.RecordDev != "" {
				d.RecordDev = s.RecordDev
			}
			settings = d
		}
	}
}

func saveSettings() error {
	settingsMu.RLock()
	s := settings
	settingsMu.RUnlock()
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath, data, 0o600)
}
