# 🐟 小双桌面宠物（XiaoShuang）

一个跨平台（Linux / Windows / macOS）的桌面 AI 陪伴形象：
**AI 生成的古风少女常驻桌面右下角**，支持表情切换、动作视频、文字对话和语音聊天。

![形象](./assets/main.png)

## ✨ 功能

- 🖼️ **桌面形象**：古风少女常驻右下角（mpv 播放，无边框置顶）
- 😊 **18 种表情动画**：开心/微笑/思考/忧愁/生气/伤心/眨眼…（AI 视频生成，镜头固定、自然还原）
- 🎬 **10 个动作视频**：抱狗荡秋千/摘野果喂狗/蝴蝶围圈/河边锦鲤/凤凰落手臂/看马摸马/躺花丛/河边戏水/蝴蝶停指尖/荡秋千
- 💬 **文字对话**：侧边栏输入文字，AI 回复（支持任意 OpenAI 兼容 API）
- 🎤 **语音聊天**：按住说话 → 本地语音识别 → AI 回复 → 语音合成朗读
- 😍 **表情联动**：对话时形象同步表情（思考→开心/忧愁）
- ⚙️ **可配置**：设置面板自定义 API 接入（DeepSeek / 通义 / 豆包 / 本地 Ollama 等）

## 🚀 快速开始

### 依赖

| 依赖 | 用途 | 安装 |
|------|------|------|
| mpv | 播放形象/音频 | `apt install mpv` / [mpv.io](https://mpv.io) |
| Python 3.9+ | 语音识别 | [python.org](https://python.org) |
| ffmpeg | 录音 | `apt install ffmpeg` / [ffmpeg.org](https://ffmpeg.org) |

### 安装语音依赖

```bash
pip install -r requirements.txt
# faster-whisper 首次运行会自动下载模型（约 1.5GB）
```

### 运行

```bash
# Linux
./fish_desktop --video-dir ./assets

# Windows（解压后）
fish_desktop.exe

# macOS
./fish_desktop
```

首次启动：
1. 右下角出现形象 + 侧边栏窗口
2. 点 ⚙️ 设置 → 填 API 地址 / Key / 模型（支持任何 OpenAI 兼容 API）
3. 在输入框聊天，或按住 🎤 说话

### 表情控制（外部调用）

```bash
echo '{"emotion":"happy"}' > mood.json
# 支持: idle/happy/smile/daze/sad/think/trance/surprised/proud/shy/sleepy/angry/excited/crying/wink/wronged/cute/scared/speechless
```

## 🔌 支持的 API 接入

| 服务 | base_url | model 示例 |
|------|---------|-----------|
| DeepSeek | `https://api.deepseek.com/v1` | deepseek-chat |
| 阿里百炼 | `https://dashscope.aliyuncs.com/compatible-mode/v1` | qwen-plus |
| 火山方舟 | `https://ark.cn-beijing.volces.com/api/v3` | doubao-seedance-2-0 |
| 硅基流动 | `https://api.siliconflow.cn/v1` | deepseek-ai/DeepSeek-V3 |
| 本地 Ollama | `http://localhost:11434/v1` | qwen2.5 |

## 🛠️ 自定义形象

见 [docs/CUSTOMIZE.md](docs/CUSTOMIZE.md) —— 用自己的 AI 生成主图、表情和动作视频。

## 📁 项目结构

```
├── mood_daemon.go    # 守护进程（形象播放+表情联动）
├── sidebar.go        # Fyne 侧边栏（对话+语音+设置）
├── settings.go       # 配置管理
├── ai_client.go      # AI 对话 / STT / TTS 调用
├── scripts/whisper_stt.py  # 本地语音识别
├── assets/           # 主图 + 表情动画 + 动作视频
└── docs/             # 文档
```

## 📜 许可

MIT License

## 🙏 致谢

- 形象与动画由 **Qwen-Image / Seedance 2.0 / 2.5**（火山方舟）生成
- UI 使用 [Fyne](https://fyne.io)
- 语音识别 [faster-whisper](https://github.com/SYSTRAN/faster-whisper)
- 语音合成 [edge-tts](https://github.com/rany2/edge-tts)
