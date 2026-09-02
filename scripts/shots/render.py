# /// script
# requires-python = ">=3.11"
# dependencies = ["rich==15.0.0"]
# ///
"""Turn one captured terminal frame into an SVG, and report its pixel size.

rich reads the ANSI back into styled text and exports the terminal it draws,
so the picture in the README is the program's own output rather than a
redrawing of it. The webfont rich embeds is dropped: GitHub blocks it, and a
screenshot should ask no CDN for anything. The local faces named instead carry
every glyph the dashboard uses, DejaVu for the geometric shapes and Meslo for
braille.
"""

import argparse
import pathlib
import re

from rich.console import Console
from rich.text import Text

FONTS = "DejaVu Sans Mono, MesloLGS Nerd Font Mono, monospace"


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("source", type=pathlib.Path, help="captured ANSI frame")
    ap.add_argument("dest", type=pathlib.Path, help="SVG to write")
    ap.add_argument("title", help="window title for the terminal chrome")
    args = ap.parse_args()

    body = args.source.read_text()
    text = Text.from_ansi(body)
    width = max(len(line) for line in text.plain.splitlines())

    with open("/dev/null", "w") as sink:
        console = Console(
            record=True,
            width=width,
            height=body.count("\n") + 1,
            file=sink,
            force_terminal=True,
            color_system="truecolor",
        )
        console.print(text, end="")
        console.save_svg(str(args.dest), title=args.title, theme=None)

    svg = re.sub(r"@font-face \{.*?\}\n", "", args.dest.read_text(), flags=re.DOTALL)
    svg = re.sub(r"font-family:[^;\n]*", f"font-family: {FONTS}", svg)
    args.dest.write_text(svg)

    box = re.search(r'viewBox="0 0 ([\d.]+) ([\d.]+)"', svg)
    if box is None:
        raise SystemExit(f"{args.dest}: rich wrote no viewBox to size the shot by")
    print(int(float(box.group(1))), int(float(box.group(2))))


if __name__ == "__main__":
    main()
