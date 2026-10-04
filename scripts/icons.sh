#!/bin/sh
# Render the PNG icon set from assets/logo.svg. Reproducible: the only input is
# the SVG and the renderer is librsvg's rsvg-convert (brew/apt: librsvg).
# Outputs are committed, so building Airrbag never needs this tool.
set -eu
cd "$(dirname "$0")/.."
command -v rsvg-convert >/dev/null || { echo "rsvg-convert not found (install librsvg)" >&2; exit 1; }
out=internal/webassets/static
mkdir -p "$out"
cp assets/logo.svg "$out/logo.svg"
cp assets/logo-mono.svg "$out/logo-mono.svg"
rsvg-convert -w 32 -h 32 assets/logo.svg -o "$out/favicon-32.png"
rsvg-convert -w 192 -h 192 assets/logo.svg -o "$out/icon-192.png"
rsvg-convert -w 512 -h 512 assets/logo.svg -o "$out/icon-512.png"
# Apple touch icons are shown on an opaque tile; give the shield a background.
rsvg-convert -w 180 -h 180 -b '#202020' assets/logo.svg -o "$out/apple-touch-icon.png"
rsvg-convert -w 512 -h 512 assets/logo.svg -o assets/logo-512.png
echo "icons written to $out"
