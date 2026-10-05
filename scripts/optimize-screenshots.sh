#!/bin/bash
# Shrink the README screenshots without visible loss: pngquant to a palette
# (UI screenshots have few colours), then keep the result only when smaller.
#   scripts/optimize-screenshots.sh [files...]   (default: docs/screenshots/*.png)
set -euo pipefail
command -v pngquant >/dev/null || { echo "pngquant not installed"; exit 1; }
files=("$@")
[ ${#files[@]} -eq 0 ] && files=(docs/screenshots/*.png)
for f in "${files[@]}"; do
    before=$(wc -c <"$f")
    pngquant --quality 85-98 --speed 1 --strip --skip-if-larger --force --output "$f" -- "$f" || true
    after=$(wc -c <"$f")
    printf '%-50s %7d -> %7d bytes\n' "$f" "$before" "$after"
done
