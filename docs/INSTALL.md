# 安装指南

## Linux

```bash
# 依赖
sudo apt install mpv ffmpeg python3-pip

# 语音依赖
pip install -r requirements.txt

# 运行
./fish_desktop --video-dir ./assets
```

## Windows

1. 下载 [mpv](https://sourceforge.net/projects/mpv-player-windows/files/)（或 mpv 便携版），把 `mpv.exe` 和 DLL 放到程序目录
2. 安装 [Python](https://python.org)（勾选 Add to PATH）
3. `pip install -r requirements.txt`
4. 双击 `fish_desktop.exe`

## macOS

1. `brew install mpv ffmpeg python3`
2. `pip install -r requirements.txt`
3. `./fish_desktop`

## 开机自启

### Linux（systemd user service）

```ini
# ~/.config/systemd/user/fish-desktop.service
[Unit]
Description=Fish Desktop Avatar
After=graphical-session.target

[Service]
ExecStart=/home/USER/fish-desktop-avatar/fish_desktop --video-dir /home/USER/fish-desktop-avatar/assets
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
```

```bash
systemctl --user daemon-reload
systemctl --user enable --now fish-desktop
```

### Windows

把 `fish_desktop.exe` 快捷方式放入启动文件夹（Win+R 输入 `shell:startup`）

### macOS

系统设置 → 通用 → 登录项 → 添加 fish_desktop

## 首次配置

启动后点侧边栏 **⚙️ 设置**：

- **API 地址 / Key / 模型**：任意 OpenAI 兼容 API（见 README 表格）
- **人设**：形象的性格设定（默认：温柔少女小双）
- **语音识别模型**：tiny/base/small/medium/large-v3（越大越准越慢）
