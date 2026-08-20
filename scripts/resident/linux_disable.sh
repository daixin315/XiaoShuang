#!/bin/bash
# 小双常驻【关闭】（Linux）：停掉服务并移除开机自启
systemctl --user disable --now fish-desktop.service 2>/dev/null || true
rm -f "$HOME/.config/systemd/user/fish-desktop.service"
systemctl --user daemon-reload
echo "✅ 小双常驻已关闭"
echo "   手动启动方式：cd $(cd "$(dirname "$0")/../.." && pwd) && ./fish_desktop"
