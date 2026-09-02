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
    """The repository root, found by walking up for go.mod."""
    here = pathlib.Path(__file__).resolve()
    for d in here.parents:
        if (d / "go.mod").is_file():
            return d
    raise SystemExit(f"cannot find the module root from {here}")


def gauntlet_home() -> pathlib.Path:
    return pathlib.Path(os.environ.get("GAUNTLET_HOME", pathlib.Path.home() / ".gauntlet"))


def agent_picks() -> dict[str, set[str]]:
    """What the triage step scheduled, per directory, across recorded runs."""
    index = gauntlet_home() / "index.jsonl"
    if not index.is_file():
        raise SystemExit(f"no runs to calibrate against: {index} is missing")
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
        directory, loop, reviews = None, 0, set()
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
        if directory and PICK_MIN <= len(reviews) <= PICK_MAX:
            picks.setdefault(directory, set()).update(reviews)
    return picks


def fast_picks(binary: pathlib.Path, directory: str) -> set[str]:
    """What --suggest-agent gauntlet proposes for one directory today."""
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
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument(
        "--detail", action="store_true", help="print each directory and what was missed"
    )
    args = ap.parse_args()

    root = module_root()
    binary = root / ".scratch" / "gauntlet-calibrate"
    binary.parent.mkdir(parents=True, exist_ok=True)
    go = shutil.which("go")
    if go is None:
        raise SystemExit("go is not on PATH")
    subprocess.run([go, "build", "-o", str(binary), "./cmd/gauntlet"], cwd=root, check=True)

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
        raise SystemExit("no --suggest runs recorded yet: nothing to calibrate against")
    recall = sum(r for r, _ in scores) / len(scores)
    precision = sum(p for _, p in scores) / len(scores)
    f1 = 2 * recall * precision / max(1e-9, recall + precision)
    print(f"{len(scores)} directories: recall={recall:.2f} precision={precision:.2f} f1={f1:.2f}")


if __name__ == "__main__":
    main()
