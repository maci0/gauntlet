# Copyright (C) 2026 Marcel W. Wysocki
# SPDX-License-Identifier: AGPL-3.0-or-later

# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""Score the file-signal suggester against what agents actually picked.

Past runs are the only labelled data this project has: a `--suggest` run
records the schedule its triage step produced, per directory. This replays
`--suggest-agent gauntlet` over those same directories and reports how much of
the agent's pick it reproduces (recall) and how much of its own proposal the
agent shared (precision).

The agent is a reference, not ground truth: it picks differently on different
days, and some of this suggester's rules (absence of tests, a review that never
changes anything here) are meant to diverge from it. Read the numbers as
movement between runs of this script, not as a grade.

    uv run scripts/suggest-calibrate.py            # build and score
    uv run scripts/suggest-calibrate.py --detail   # per directory, with misses
"""

import argparse
import json
import os
import pathlib
import shutil
import subprocess

# A suggest run that scheduled nearly the whole catalog is a fallback, not a
# pick: the triage step failed and the run went ahead with everything.
PICK_MIN = 3
PICK_MAX = 30


def module_root() -> pathlib.Path:
    """Find the repository root by walking up for go.mod."""
    here = pathlib.Path(__file__).resolve()
    for d in here.parents:
        if (d / "go.mod").is_file():
            return d
    msg = f"cannot find the module root from {here}"
    raise SystemExit(msg)


def gauntlet_home() -> pathlib.Path:
    """Locate the run journal, honoring GAUNTLET_HOME when it is set."""
    raw = os.environ.get("GAUNTLET_HOME")
    if raw and raw.strip():
        return pathlib.Path(raw.strip()).expanduser()
    return pathlib.Path.home() / ".gauntlet"


def run_picks(run: pathlib.Path) -> tuple[str, set[str]]:
    """Read one recorded run: its directory and the reviews it started in loop 1."""
    directory: str = ""
    loop, reviews = 0, set()
    for entry in run.read_text(encoding="utf-8", errors="replace").splitlines():
        try:
            event = json.loads(entry)
        except json.JSONDecodeError:
            continue
        match event.get("ev"):
            case "run_start":
                directory = event.get("dir")
            case "loop_start":
                loop = event.get("loop", 0)
            case "review_start" if loop == 1:
                reviews.add(event.get("review"))
    return directory, reviews


def agent_picks() -> dict[str, set[str]]:
    """Collect what the triage step scheduled, per directory, across recorded runs."""
    index = gauntlet_home() / "index.jsonl"
    if not index.is_file():
        msg = f"no runs to calibrate against: {index} is missing"
        raise SystemExit(msg)
    picks: dict[str, set[str]] = {}
    for line in index.read_text(encoding="utf-8", errors="replace").splitlines():
        try:
            summary = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not any(a in ("--suggest", "-s") for a in summary.get("args") or []):
            continue
        run = pathlib.Path(summary.get("path", ""))
        if not run.is_file():
            continue
        directory, reviews = run_picks(run)
        if directory and PICK_MIN <= len(reviews) <= PICK_MAX:
            picks.setdefault(directory, set()).update(reviews)
    return picks


def fast_picks(binary: pathlib.Path, directory: str) -> set[str]:
    """List what --suggest-agent gauntlet proposes for one directory today."""
    proc = subprocess.run(
        [
            str(binary),
            "-C",
            directory,
            "--suggest",
            "--suggest-agent",
            "gauntlet",
            "--dry-run",
        ],
        capture_output=True,
        text=True,
        check=False,
    )
    return {
        line.strip().split()[0]
        for line in proc.stdout.splitlines()
        if line.startswith("  ") and "-review" in line
    }


def main() -> None:
    """Build the binary, replay the suggester, and print recall and precision."""
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "--detail", action="store_true", help="print each directory and what was missed"
    )
    args = ap.parse_args()

    root = module_root()
    binary = root / ".scratch" / "gauntlet-calibrate"
    binary.parent.mkdir(parents=True, exist_ok=True)
    make = shutil.which("make")
    if make is None:
        msg = "make is not on PATH"
        raise SystemExit(msg)
    # `make build` is the only build of this binary the project maintains, and
    # it owns the tag set, the ldflags, and the environment the go build needs
    # (CGO_ENABLED, GOWORK, GOTOOLCHAIN, -mod=readonly). Spelling the go build
    # out here instead left this scoring a binary no release would ever ship:
    # the Makefile's default TAGS is sqlite, and this one had no tags at all,
    # so a number produced against the wrong flavor of the same source was
    # being compared to the previous run's. `make build` writes ./gauntlet;
    # move it rather than building a second copy of the tree.
    subprocess.run(
        [make, "--no-print-directory", "build"],
        cwd=root,
        check=True,
    )
    shutil.move(root / "gauntlet", binary)

    scores: list[tuple[float, float]] = []
    for directory, picked in sorted(agent_picks().items()):
        if not pathlib.Path(directory).is_dir():
            continue
        proposed = fast_picks(binary, directory)
        shared = len(picked & proposed)
        recall = shared / len(picked)
        precision = shared / max(1, len(proposed))
        scores.append((recall, precision))
        if args.detail:
            print(
                f"{pathlib.Path(directory).name:<16} "
                f"agent={len(picked):>3} fast={len(proposed):>3} "
                f"recall={recall:.2f} precision={precision:.2f}"
            )
            print(f"   missed: {sorted(picked - proposed)}")
    if not scores:
        msg = "no --suggest runs recorded yet: nothing to calibrate against"
        raise SystemExit(msg)
    recall = sum(r for r, _ in scores) / len(scores)
    precision = sum(p for _, p in scores) / len(scores)
    f1 = 2 * recall * precision / max(1e-9, recall + precision)
    print(f"{len(scores)} directories: recall={recall:.2f} precision={precision:.2f} f1={f1:.2f}")


if __name__ == "__main__":
    main()
