#!/bin/bash
# 小双常驻【开启】（Linux）：安装 systemd user service，开机自启 + 崩溃自动拉起
# 使用 Xvfb :99 虚拟显示器（无物理显示器也能跑；真实桌面恢复后可改回 :0）
set -e
APP_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
SERVICE_DIR="$HOME/.config/systemd/user"
XVFB_SERVICE="$SERVICE_DIR/xvfb.service"
SERVICE="$SERVICE_DIR/fish-desktop.service"

# 先停掉手动跑的实例，避免端口 8721 冲突
pkill -f "$APP_DIR/fish_desktop" 2>/dev/null || true
sleep 1

mkdir -p "$SERVICE_DIR"

# Xvfb 虚拟显示器 :99
cat > "$XVFB_SERVICE" <<EOF
[Unit]
Description=Xvfb 虚拟显示器 (:99，无物理显示器时给小双提供屏幕)

[Service]
Type=simple
ExecStart=/usr/bin/Xvfb :99 -ac -screen 0 1280x800x24 +extension GLX +render -noreset
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
EOF

# 小双服务（连虚拟屏 :99；FISH_NO_TRAY=1 跳过托盘——虚拟屏无托盘 watcher）
cat > "$SERVICE" <<EOF
[Unit]
Description=XiaoShuang (小双)
After=graphical-session.target xvfb.service
Wants=xvfb.service

[Service]
Type=simple
Environment=DISPLAY=:99
Environment=FISH_NO_TRAY=1
ExecStart=$APP_DIR/fish_desktop
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now xvfb.service
systemctl --user enable --now fish-desktop.service
echo ""
echo "✅ 小双常驻已开启（Xvfb :99 虚拟屏 + systemd 自启/崩溃拉起）"
echo "   注意：虚拟屏无托盘，关闭窗口不会隐藏（FISH_NO_TRAY=1）"
echo "   想用真实桌面时：改 service 里 DISPLAY=:0 并去掉 FISH_NO_TRAY=1"
systemctl --user status fish-desktop.service --no-pager | head -6
