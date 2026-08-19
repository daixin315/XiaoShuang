#!/bin/bash
# 设置桌面形象表情（外部联动时更新 mood.json）
# 用法: set_mood.sh <emotion> [mood_file]
#   emotion: idle/happy/smile/daze/sad/think/trance/surprised/proud/shy/sleepy/angry/excited/crying/wink/wronged/cute/scared/speechless
if [ -z "$1" ]; then
  echo "用法: set_mood.sh <emotion> [mood_file]"
  exit 1
fi
MOOD_FILE="${2:-$(dirname "$0")/mood.json}"
echo "{\"emotion\": \"$1\"}" > "$MOOD_FILE"
echo "✅ 表情已设为: $1 → $MOOD_FILE"
