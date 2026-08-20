#!/bin/bash
# 小双常驻【开启】（macOS）：安装 launchd LaunchAgent，登录自启 + KeepAlive
set -e
APP_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
PLIST_DIR="$HOME/Library/LaunchAgents"
PLIST="$PLIST_DIR/com.fish.desktop.plist"

mkdir -p "$PLIST_DIR"
cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.fish.desktop</string>
    <key>ProgramArguments</key>
    <array>
        <string>$APP_DIR/fish_desktop</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
</dict>
</plist>
EOF

launchctl unload "$PLIST" 2>/dev/null || true
launchctl load "$PLIST"
echo "✅ 小双常驻已开启（LaunchAgent：登录自启 + 崩溃自动拉起）"
