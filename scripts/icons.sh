#!/usr/bin/env bash
# Regenerates the committed PWA icons from the Paperlens header logo.
# Requires rsvg-convert (librsvg). Run from the repository root.
set -euo pipefail

out=internal/web/static/icons
accent='#315f85'
command -v rsvg-convert >/dev/null || { echo "rsvg-convert is required (brew install librsvg)" >&2; exit 1; }
mkdir -p "$out"

# svg SCALE ROUNDED: logo (viewBox 0 0 32 32, visual centre 17.5,17) scaled
# around the icon centre on an accent background.
svg() {
  local scale=$1 radius=$2
  local tx ty
  tx=$(awk -v s="$scale" 'BEGIN { printf "%.3f", 16 - 17.5 * s }')
  ty=$(awk -v s="$scale" 'BEGIN { printf "%.3f", 16 - 17 * s }')
  cat <<SVG
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32">
<rect width="32" height="32" rx="$radius" fill="$accent"/>
<g transform="translate($tx $ty) scale($scale)" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
<rect x="4" y="3" width="18" height="24" rx="3" fill="#fff"/>
<path d="M9 10h8M9 15h5" fill="none" stroke="$accent"/>
<circle cx="22" cy="22" r="6" fill="$accent" stroke="#fff"/>
<path d="m27 27 4 4" fill="none" stroke="#fff"/>
</g>
</svg>
SVG
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Rounded icon: favicon and manifest "any" purpose.
svg 0.7 7 > "$out/icon.svg"
rsvg-convert -w 192 -h 192 "$out/icon.svg" -o "$out/icon-192.png"
rsvg-convert -w 512 -h 512 "$out/icon.svg" -o "$out/icon-512.png"
# Maskable: full bleed, logo inside the 80% safe zone.
svg 0.56 0 > "$tmp/maskable.svg"
rsvg-convert -w 512 -h 512 "$tmp/maskable.svg" -o "$out/icon-maskable-512.png"
# Apple touch icon: full bleed and opaque; iOS applies its own mask.
svg 0.68 0 > "$tmp/apple.svg"
rsvg-convert -w 180 -h 180 -b "$accent" "$tmp/apple.svg" -o "$out/apple-touch-icon.png"
echo "Icons written to $out"
