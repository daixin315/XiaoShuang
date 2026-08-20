#!/bin/bash
# 小双常驻【开启】（Linux）：安装 systemd user service，开机自启 + 崩溃自动拉起
set -e
APP_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
SERVICE_DIR="$HOME/.config/systemd/user"
SERVICE="$SERVICE_DIR/fish-desktop.service"

# 先停掉手动跑的实例，避免端口 8721 冲突
pkill -f "$APP_DIR/fish_desktop" 2>/dev/null || true
sleep 1

mkdir -p "$SERVICE_DIR"
cat > "$SERVICE" <<EOF
[Unit]
Description=Fish Desktop Avatar (小双)
After=graphical-session.target

[Service]
Type=simple
Environment=DISPLAY=:0
ExecStart=$APP_DIR/fish_desktop
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
EOF

systemctl --user daemon-reload
systemctl --user enable --now fish-desktop.service
echo ""
echo "✅ 小双常驻已开启（systemd user service，开机自启 + 崩溃自动拉起）"
systemctl --user status fish-desktop.service --no-pager | head -6
