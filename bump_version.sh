#!/bin/bash
# Auto-bump version: year.month.day.sequence
# Usage: ./bump_version.sh

VERSION_FILE="$(dirname "$0")/VERSION"
TODAY=$(date +%Y.%-m.%-d)

CURRENT=$(cat "$VERSION_FILE" 2>/dev/null)
if [[ "$CURRENT" == $TODAY.* ]]; then
    # Same day, increment sequence
    SEQ=$(echo "$CURRENT" | sed "s/$TODAY\.//")
    NEW_SEQ=$((SEQ + 1))
    echo "${TODAY}.${NEW_SEQ}" > "$VERSION_FILE"
else
    # New day, start from 1
    echo "${TODAY}.1" > "$VERSION_FILE"
fi

echo "Version: $(cat "$VERSION_FILE")"
