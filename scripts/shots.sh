#!/usr/bin/env bash
# Regenerate the README screenshots from the program's own output.
#
#   ANSI  the renderer writes its frames (internal/ui/shots_test.go)
#   SVG   scripts/shots/render.py exports the terminal rich draws from them
#   PNG   a browser rasterizes that, because it has the font fallback the
#         box-drawing, braille, and geometric glyphs need
#
# Needs uv, chromium, and ImageMagick. This is a maintainer task, not part of
# the build: the checked-in PNGs are what the README uses.
set -euo pipefail

# Walk up to the module root rather than assuming where this script sits.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
while [ ! -f "$root/go.mod" ] && [ "$root" != "/" ]; do
	root="$(dirname "$root")"
done
[ -f "$root/go.mod" ] || { echo "cannot find the module root from ${BASH_SOURCE[0]}" >&2; exit 1; }
# Disk, not the system temp dir: a shot is a few megabytes of PNG and /tmp is
# RAM here. .scratch is gitignored and is where this project's scratch lives.
work="$root/.scratch/shots"
rm -rf "$work"
mkdir -p "$work"

CLICOLOR_FORCE=1 SHOT_DIR="$work" go test "$root/internal/ui" -run TestWriteShots -count=1 >/dev/null

for shot in 'dashboard:gauntlet --tui' 'launcher:gauntlet pick'; do
	name="${shot%%:*}"
	title="${shot#*:}"

	read -r w h < <(uv run --quiet "$root/scripts/shots/render.py" \
		"$work/$name.ansi" "$work/$name.svg" "$title")

	printf '<html><body style="margin:0;background:#1e1e2e"><img src="%s.svg" width="%s" height="%s"></body></html>' \
		"$name" "$w" "$h" > "$work/$name.html"
	chromium --headless --disable-gpu --hide-scrollbars \
		--force-device-scale-factor=2 --window-size="$w,$h" \
		--screenshot="$work/$name.raw.png" "$work/$name.html" >/dev/null 2>&1

	magick "$work/$name.raw.png" -strip -colors 128 \
		-define png:compression-level=9 "$root/assets/$name.png"
	echo "assets/$name.png"
done
