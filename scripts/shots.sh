#!/usr/bin/env bash
# Regenerate the README screenshots from the program's own output.
#
#   ANSI  the renderer writes its frames (internal/ui/shots_test.go)
#   SVG   rich reads the ANSI back and exports the terminal it draws
#   PNG   a browser rasterizes that, because it has the font fallback the
#         box-drawing, braille, and geometric glyphs need
#
# Needs uv, chromium, and ImageMagick. This is a maintainer task, not part of
# the build: the checked-in PNGs are what the README uses.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

CLICOLOR_FORCE=1 SHOT_DIR="$work" go test "$root/internal/ui" -run TestWriteShots -count=1 >/dev/null

cat > "$work/ansi2svg.py" <<'PY'
import pathlib
import sys

from rich.console import Console
from rich.text import Text

src, dst, title = sys.argv[1], sys.argv[2], sys.argv[3]
body = pathlib.Path(src).read_text()
text = Text.from_ansi(body)
width = max(len(line) for line in text.plain.splitlines())
console = Console(record=True, width=width, height=body.count("\n") + 1,
                  file=open("/dev/null", "w"), force_terminal=True,
                  color_system="truecolor")
console.print(text, end="")
console.save_svg(dst, title=title, theme=None)
PY

cat > "$work/localfonts.py" <<'PY'
import pathlib
import re
import sys

# Drop rich's remote webfont: GitHub blocks it, and a screenshot should ask no
# CDN for anything. The local faces named here carry every glyph the dashboard
# draws, DejaVu for the geometric shapes and Meslo for braille.
p = pathlib.Path(sys.argv[1])
s = re.sub(r"@font-face \{.*?\}\n", "", p.read_text(), flags=re.S)
s = re.sub(r"font-family:[^;\n]*",
           "font-family: DejaVu Sans Mono, MesloLGS Nerd Font Mono, monospace", s)
p.write_text(s)
PY

cat > "$work/size.py" <<'PY'
import pathlib
import re
import sys

m = re.search(r'viewBox="0 0 ([\d.]+) ([\d.]+)"', pathlib.Path(sys.argv[1]).read_text())
print(int(float(m.group(1))), int(float(m.group(2))))
PY

for shot in 'dashboard:gauntlet --tui' 'launcher:gauntlet pick'; do
	name="${shot%%:*}"
	title="${shot#*:}"

	uv run --quiet --with rich python "$work/ansi2svg.py" \
		"$work/$name.ansi" "$work/$name.svg" "$title"
	python3 "$work/localfonts.py" "$work/$name.svg"

	read -r w h < <(python3 "$work/size.py" "$work/$name.svg")
	printf '<html><body style="margin:0;background:#1e1e2e"><img src="%s.svg" width="%s" height="%s"></body></html>' \
		"$name" "$w" "$h" > "$work/$name.html"
	chromium --headless --disable-gpu --hide-scrollbars \
		--force-device-scale-factor=2 --window-size="$w,$h" \
		--screenshot="$work/$name.raw.png" "$work/$name.html" >/dev/null 2>&1

	magick "$work/$name.raw.png" -strip -colors 128 \
		-define png:compression-level=9 "$root/assets/$name.png"
	echo "assets/$name.png"
done
