#!/usr/bin/env bash
# Regenerate the README screenshots from the program's own output.
#
#   ANSI  the renderer writes its frames (internal/ui/shots_test.go)
#   SVG   scripts/shots/render.py exports the terminal rich draws from them
#   PNG   a browser rasterizes that, because it has the font fallback the
#         box-drawing, braille, and geometric glyphs need
#
# Needs uv, a Chromium-based browser, and ImageMagick. This is a maintainer
# task, not part of the build: the checked-in PNGs are what the README uses.
set -euo pipefail
export GOFLAGS="-mod=readonly"
export GOWORK=off
export GOTOOLCHAIN=local
export CGO_ENABLED=0
# The PNGs are checked in, so they must not follow the host the maintainer
# happens to be on. LC_ALL fixes collation, TZ fixes any date the renderer
# prints. The Makefile pins the same two for everything it runs.
export LC_ALL=C
export TZ=UTC

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

# Linux boxes usually have `chromium` on PATH; macOS typically has Chromium
# or Google Chrome as an .app. Either rasterizes the SVG.
browser=""
for c in chromium chromium-browser google-chrome google-chrome-stable; do
	if command -v "$c" >/dev/null 2>&1; then
		browser="$c"
		break
	fi
done
if [ -z "$browser" ]; then
	for c in \
		"/Applications/Chromium.app/Contents/MacOS/Chromium" \
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"; do
		if [ -x "$c" ]; then
			browser="$c"
			break
		fi
	done
fi
[ -n "$browser" ] || {
	echo "chromium or Google Chrome is required" >&2
	exit 1
}

if command -v magick >/dev/null 2>&1; then
	im=magick
elif command -v convert >/dev/null 2>&1; then
	im=convert
else
	echo "ImageMagick is required (magick or convert)" >&2
	exit 1
fi

# The screenshots come from the build configuration `make build` ships, the
# Makefile's default TAGS. The two agree today; the tag is here so a future
# tag-gated code path cannot quietly change a picture out from under the
# checked-in PNGs, which nothing else would notice.
CLICOLOR_FORCE=1 SHOT_DIR="$work" go test -tags sqlite "$root/internal/ui" -run TestWriteShots -count=1 >/dev/null

for shot in 'dashboard:gauntlet --tui' 'launcher:gauntlet pick'; do
	name="${shot%%:*}"
	title="${shot#*:}"

	# A process substitution masks the renderer's exit status, so a failed
	# render used to leave w and h empty and rasterize an empty page. Take
	# the output as a value instead and stop on a non-zero status.
	if ! size="$(uv run --quiet "$root/scripts/shots/render.py" \
		"$work/$name.ansi" "$work/$name.svg" "$title")"; then
		echo "$name: render failed" >&2
		exit 1
	fi
	read -r w h <<<"$size"
	# Spelled as an if, not `A && B || C`: shellcheck's SC2015 is right that the
	# `||` branch also runs when the left side fails for another reason, and the
	# runner image carries an older shellcheck than the pin that reports it.
	if [ -z "$w" ] || [ -z "$h" ]; then
		echo "$name: renderer printed no size" >&2
		exit 1
	fi

	printf '<html><body style="margin:0;background:#1e1e2e"><img src="%s.svg" width="%s" height="%s"></body></html>' \
		"$name" "$w" "$h" > "$work/$name.html"
	# A unique user-data-dir keeps headless Chrome on macOS from failing
	# on a locked default profile. Its output and status are both discarded
	# so nothing lands in the terminal, which means a browser that exits
	# non-zero without a screenshot used to be reported as an ImageMagick
	# failure on a file that was never written.
	if ! "$browser" --headless --disable-gpu --hide-scrollbars \
		--user-data-dir="$work/chrome-$name" \
		--force-device-scale-factor=2 --window-size="$w,$h" \
		--screenshot="$work/$name.raw.png" "$work/$name.html" >/dev/null 2>&1 ||
		[ ! -s "$work/$name.raw.png" ]; then
		echo "$name: $browser failed to rasterize $work/$name.html" >&2
		exit 1
	fi

	"$im" "$work/$name.raw.png" -strip -colors 128 \
		-define png:compression-level=9 "$root/assets/$name.png"
	echo "assets/$name.png"
done
