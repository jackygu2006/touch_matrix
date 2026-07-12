#!/bin/bash
# 自动更新版本号：年.月.日.当日序号
# 用法: ./bump_version.sh

VERSION_FILE="$(dirname "$0")/VERSION"
TODAY=$(date +%Y.%-m.%-d)

CURRENT=$(cat "$VERSION_FILE" 2>/dev/null)
if [[ "$CURRENT" == $TODAY.* ]]; then
    # 同一天，递增序号
    SEQ=$(echo "$CURRENT" | sed "s/$TODAY\.//")
    NEW_SEQ=$((SEQ + 1))
    echo "${TODAY}.${NEW_SEQ}" > "$VERSION_FILE"
else
    # 新的一天，从 1 开始
    echo "${TODAY}.1" > "$VERSION_FILE"
fi

echo "Version: $(cat "$VERSION_FILE")"
