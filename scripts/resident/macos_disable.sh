#!/bin/bash
# 小双常驻【关闭】（macOS）：卸载 LaunchAgent
PLIST="$HOME/Library/LaunchAgents/com.fish.desktop.plist"
launchctl unload "$PLIST" 2>/dev/null || true
rm -f "$PLIST"
echo "✅ 小双常驻已关闭"
echo "   手动启动方式：cd $(cd "$(dirname "$0")/../.." && pwd) && ./fish_desktop"
