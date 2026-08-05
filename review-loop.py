#!/usr/bin/env python3
"""Run review prompts via claude/gemini/qwen/codex/grok/agy/cursor-agent against current dir."""

from __future__ import annotations

import argparse
import fcntl
import os
import random
import re
import shutil
import signal
import subprocess
import sys
import time
from collections import defaultdict
from dataclasses import dataclass, field
from pathlib import Path

VALID_TOOLS = {"claude", "gemini", "qwen", "codex", "grok", "agy", "cursor-agent"}
NO_MODEL_TOOLS = {"agy"}

PROMPT_HEADER = "MODE: AUTO_FIX — apply fixes directly to files. Do NOT write a report."

PROMPT_SUFFIX = """

---

OVERRIDE: Ignore any 'Output format', 'Executive Summary', or report-style sections in the instructions above. Do NOT write a report.

Operating mode: apply fixes directly to files, autonomously.

Rules:
- First check if this review makes sense for this codebase. If not, exit immediately without changes.
- Use the best available tool for search and rewrite (check PATH; fall back to grep/sed only when none exist):
  - `rg` (ripgrep) for plain-text and literal searches.
  - `ast-grep`/`sg` for syntax-aware structural search and pattern-based rewrite.
  - `patchwork` for AST-native sed: find/replace/delete/insert by structure.
  - `tsed` for tree-sitter operations modeled after sed.
  - `semcode` (if a `.semcode.db` index exists or `semcode-index` is available) for semantic C/C++/Rust queries: find_function, callers/callees, call chains, type definitions.
- Make the smallest reversible diff possible.
- Fix at most ~10 highest-value issues this pass. Stop after that.
- Do not delete tests.
- Do not change public APIs or exported symbols.
- Skip anything uncertain. Never ask questions.
- Before fixing an issue, prove it is real: trace the actual code path, read the full function (not just a fragment), and verify callers/config branches don't already handle it. Comments and names can lie; verify against the implementation. If you cannot prove it, skip it.
- Do not add defensive checks (null/bounds/validation) unless you can name a concrete path where invalid data reaches the code.
- Never modify files in: node_modules, vendor, dist, build, .next, target, .git, generated/auto-generated files. Never hand-edit lockfiles (package-lock.json, yarn.lock, pnpm-lock.yaml, Cargo.lock, go.sum, etc.); they may only change as the output of running the project's package manager (npm/pnpm/yarn/cargo/go/uv).
- If a single fix would change more than 300 lines in one file, skip it.
- Do not commit anything to git. Do not run git commit, git push, or git tag.
- Never modify or delete .review-loop.lock or any *-review.md prompt files.
- After edits, if the project has a lint, typecheck, or test command configured (package.json scripts, Makefile, justfile, pyproject.toml, etc.), run the relevant one. If it fails due to your changes, revert them.
- At the end, print a brief 1-line summary per changed file in the form: 'PATH — what was done'. If nothing changed, print 'No changes.'"""


@dataclass(frozen=True)
class ToolSpec:
    tool: str
    model: str | None = None

    def label(self) -> str:
        return f"{self.tool}:{self.model}" if self.model else self.tool


@dataclass
class ReviewResult:
    review: str
    tool: ToolSpec
    elapsed: float
    status: str  # "ok", "fail", "timeout", "interrupted", "skipped"
    exit_code: int | None = None


@dataclass
class Stats:
    results: list[ReviewResult] = field(default_factory=list)

    def add(self, result: ReviewResult) -> None:
        self.results.append(result)

    @property
    def ok_count(self) -> int:
        return sum(1 for r in self.results if r.status == "ok")

    @property
    def fail_count(self) -> int:
        return sum(1 for r in self.results if r.status in ("fail", "timeout"))

    @property
    def total_count(self) -> int:
        return len(self.results)

    def tool_summary(self) -> dict[str, dict[str, int]]:
        summary: dict[str, dict[str, int]] = defaultdict(lambda: defaultdict(int))
        for r in self.results:
            summary[r.tool.label()][r.status] += 1
        return dict(summary)


SKIP_DIRS = {
    "node_modules", "vendor", "dist", "build", ".next", "target", ".git",
    "__pycache__", ".venv", "venv", ".tox", ".cache",
}


def discover_reviews(prompt_dir: Path) -> dict[str, Path]:
    """Map review name -> prompt file.

    Bundled prompts from prompt_dir, plus any *-review.md found in the
    project tree (cwd). A project-local prompt wins over a bundled one of
    the same name; among project duplicates the first found in the walk
    (depth-first, directories and files sorted) wins. Symlinks are never
    followed: prompts must be regular files inside the tree, so a link
    cannot pull out-of-tree content into a permission-bypassed AI run.
    """
    reviews: dict[str, Path] = {}
    if prompt_dir.is_dir():
        for f in sorted(prompt_dir.glob("*-review.md")):
            reviews[f.stem] = f
    project = Path.cwd().resolve()
    prompt_dir = prompt_dir.resolve()
    project_seen: set[str] = set()
    for root, dirs, files in os.walk(project):
        dirs[:] = sorted(d for d in dirs if d not in SKIP_DIRS)
        for name in sorted(files):
            if not name.endswith("-review.md"):
                continue
            f = Path(root) / name
            if f.is_symlink() or f.parent.resolve() == prompt_dir:
                continue
            if re.search(r"[\x00-\x1f\x7f]", f.stem):
                continue
            if f.stem in project_seen:
                print(
                    sanitize(
                        f"Warning: duplicate project prompt {f.stem!r}: "
                        f"using {reviews[f.stem]}, ignoring {f}"
                    ),
                    file=sys.stderr,
                )
                continue
            project_seen.add(f.stem)
            reviews[f.stem] = f
    return dict(sorted(reviews.items()))


def parse_duration(s: str) -> int:
    m = re.fullmatch(r"(\d+)([smhd]?)", s)
    if not m:
        raise argparse.ArgumentTypeError(f"invalid duration: {s} (e.g. 30m, 1h, 90s)")
    n, unit = int(m.group(1)), m.group(2)
    secs = n * {"": 1, "s": 1, "m": 60, "h": 3600, "d": 86400}[unit]
    if secs <= 0:
        raise argparse.ArgumentTypeError(f"duration must be positive: {s}")
    return secs


MIXED_KEYWORDS = {"mixed", "random", "all"}


def parse_agents(s: str) -> list[ToolSpec]:
    specs: list[ToolSpec] = []
    seen: set[ToolSpec] = set()
    for entry in s.split(","):
        entry = entry.strip()
        if not entry:
            continue
        if entry.lower() in MIXED_KEYWORDS:
            installed = installed_tools()
            if not installed:
                raise argparse.ArgumentTypeError(
                    f"'{entry}' matched no installed tools "
                    f"(supported: {', '.join(sorted(VALID_TOOLS))})"
                )
            for spec in installed:
                if spec not in seen:
                    specs.append(spec)
                    seen.add(spec)
            continue
        tool, _, model = entry.partition(":")
        tool = tool.strip()
        model = model.strip() or None
        if tool not in VALID_TOOLS:
            raise argparse.ArgumentTypeError(
                f"unknown tool: {tool!r} "
                f"(valid: {', '.join(sorted(VALID_TOOLS))}, "
                f"or {'/'.join(sorted(MIXED_KEYWORDS))} for all)"
            )
        if tool in NO_MODEL_TOOLS and model:
            raise argparse.ArgumentTypeError(
                f"{tool} does not support specifying a model: {entry!r}"
            )
        spec = ToolSpec(tool, model)
        if spec not in seen:
            specs.append(spec)
            seen.add(spec)
    if not specs:
        raise argparse.ArgumentTypeError("no agents specified")
    return specs


def fmt_duration(secs: float) -> str:
    secs = int(secs)
    if secs >= 3600:
        return f"{secs // 3600}h{(secs % 3600) // 60:02d}m"
    return f"{secs // 60}m{secs % 60:02d}s"


def installed_tools() -> list[ToolSpec]:
    return [ToolSpec(t) for t in sorted(VALID_TOOLS) if shutil.which(t)]


def sanitize(s: str) -> str:
    """Strip terminal control characters from untrusted display text."""
    return re.sub(r"[\x00-\x1f\x7f]", "", s)


def log(msg: str) -> None:
    print(f"[{time.strftime('%H:%M:%S')}] {sanitize(msg)}", flush=True)


def usage_error(msg: str) -> None:
    print(msg, file=sys.stderr)
    sys.exit(2)


def check_tool(name: str) -> None:
    if shutil.which(name) is None:
        usage_error(f"Required tool not found in PATH: {name}")


def build_cmd(spec: ToolSpec, prompt: str) -> list[str]:
    if spec.tool == "claude":
        cmd = ["claude", "--dangerously-skip-permissions"]
        if spec.model:
            cmd += ["--model", spec.model]
        cmd += ["-p", prompt]
    elif spec.tool in ("gemini", "qwen"):
        cmd = [spec.tool, "-y"]
        if spec.model:
            cmd += ["-m", spec.model]
        cmd += ["-p", prompt]
    elif spec.tool == "codex":
        cmd = ["codex", "exec", "--dangerously-bypass-approvals-and-sandbox"]
        if spec.model:
            cmd += ["-m", spec.model]
        cmd.append(prompt)
    elif spec.tool == "grok":
        cmd = ["grok", "--permission-mode", "bypassPermissions"]
        if spec.model:
            cmd += ["-m", spec.model]
        cmd += ["-p", prompt]
    elif spec.tool == "agy":
        cmd = ["agy", "--dangerously-skip-permissions", "-p", prompt]
    elif spec.tool == "cursor-agent":
        cmd = ["cursor-agent", "--print", "-f"]
        if spec.model:
            cmd += ["--model", spec.model]
        cmd.append(prompt)
    else:
        raise ValueError(f"unknown tool: {spec.tool}")
    return cmd


class Runner:
    def __init__(self, args: argparse.Namespace):
        self.args = args
        self.tools: list[ToolSpec] = args.agents
        self.reviews: list[str] = self._filter_reviews()
        self.timeout_secs: int = args.timeout
        self.bundled_dir = args.prompt_dir.resolve()
        self.stats = Stats()
        self.loop_count = 0
        self.stopping = False
        self.interrupt_count = 0
        self.current_proc: subprocess.Popen | None = None
        self.script_start = time.monotonic()

    def _filter_reviews(self) -> list[str]:
        self.prompt_files = discover_reviews(self.args.prompt_dir)
        available = list(self.prompt_files)
        if not available:
            usage_error(f"No *-review.md files found in: {self.args.prompt_dir}")

        reviews = list(available)

        def split(arg: str) -> set[str]:
            return {x.strip() for x in arg.split(",") if x.strip()}

        if self.args.reviews:
            inc = split(self.args.reviews)
            unknown = inc - set(available)
            if unknown:
                usage_error(
                    f"Unknown review(s): {', '.join(sorted(unknown))}\n"
                    f"Available: {', '.join(available)}"
                )
            reviews = [r for r in reviews if r in inc]
        if self.args.exclude:
            exc = split(self.args.exclude)
            unknown = exc - set(available)
            if unknown:
                usage_error(
                    f"Unknown review(s) in --exclude: {', '.join(sorted(unknown))}\n"
                    f"Available: {', '.join(available)}"
                )
            reviews = [r for r in reviews if r not in exc]
        if not reviews:
            usage_error("No reviews remain after filtering.")
        return reviews

    def pick_tool(self) -> ToolSpec:
        return random.choice(self.tools)

    def _origin(self, prompt_file: Path) -> str:
        return "" if prompt_file.parent.resolve() == self.bundled_dir else " [project]"

    def run_review(self, review: str) -> None:
        spec = self.pick_tool()
        prompt_file = self.prompt_files[review]
        if prompt_file.is_symlink():  # re-check: swapped since discovery
            log(f"Prompt file became a symlink: {prompt_file} — skipping")
            self.stats.add(ReviewResult(review, spec, 0.0, "skipped"))
            return
        try:
            text = prompt_file.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as e:
            log(f"Cannot read prompt file {prompt_file} ({e}) — skipping")
            self.stats.add(ReviewResult(review, spec, 0.0, "skipped"))
            return

        prompt = f"{PROMPT_HEADER}\n\n{text}{PROMPT_SUFFIX}"

        start = time.monotonic()
        log(f"Running {review}{self._origin(prompt_file)} with {spec.label()} (timeout {fmt_duration(self.timeout_secs)})")

        cmd = build_cmd(spec, prompt)
        if self.stopping:
            return
        try:
            proc = subprocess.Popen(cmd, start_new_session=True)
        except OSError as e:
            log(f"FAILED to launch {spec.label()} for {review}: {e}")
            self.stats.add(ReviewResult(review, spec, 0.0, "fail"))
            return
        self.current_proc = proc
        if self.stopping:  # signal landed between Popen and registration
            self._kill_proc(proc, signal.SIGTERM)
        timed_out = False
        try:
            rc = proc.wait(timeout=10 if self.stopping else self.timeout_secs)
        except subprocess.TimeoutExpired:
            timed_out = not self.stopping
            if timed_out:
                log(f"TIMEOUT: {review} ({spec.label()}) after {fmt_duration(self.timeout_secs)}")
            self._kill_proc(proc, signal.SIGTERM)
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self._kill_proc(proc, signal.SIGKILL)
                proc.wait()
            rc = -signal.SIGTERM
        finally:
            self.current_proc = None

        elapsed = time.monotonic() - start
        self.interrupt_count = 0

        if timed_out:
            self.stats.add(ReviewResult(review, spec, elapsed, "timeout"))
            return

        if self.stopping and (rc < 0 or rc in (130, 143)):
            log(f"Interrupted: {review} ({spec.label()}) after {fmt_duration(elapsed)}")
            self.stats.add(ReviewResult(review, spec, elapsed, "interrupted", rc))
        elif rc != 0:
            log(f"FAILED: {review} ({spec.label()}) after {fmt_duration(elapsed)} — exit code {rc}")
            self.stats.add(ReviewResult(review, spec, elapsed, "fail", rc))
        else:
            log(f"Done: {review} ({spec.label()}) in {fmt_duration(elapsed)}")
            self.stats.add(ReviewResult(review, spec, elapsed, "ok", 0))

    def _kill_proc(self, proc: subprocess.Popen, sig: int) -> None:
        try:
            os.killpg(os.getpgid(proc.pid), sig)
        except (ProcessLookupError, PermissionError):
            try:
                proc.send_signal(sig)
            except ProcessLookupError:
                pass

    def handle_signal(self, signum, frame) -> None:
        # Async-signal-safe: os.write only; print/log here can raise
        # "reentrant call" if the signal lands mid-print on the main thread.
        self.interrupt_count += 1
        self.stopping = True
        proc = self.current_proc
        if proc and proc.poll() is None:
            if self.interrupt_count == 1:
                os.write(2, b"\nSignal received - terminating current review. Ctrl+C again for KILL.\n")
                self._kill_proc(proc, signal.SIGTERM)
            else:
                os.write(2, b"\nForce-killing current review...\n")
                self._kill_proc(proc, signal.SIGKILL)
        else:
            os.write(2, b"\nSignal received - stopping...\n")

    def print_stats(self) -> None:
        total = time.monotonic() - self.script_start
        print()
        print("=== Review loop stopped ===")
        print(f"Completed loops: {self.loop_count}")
        print(f"Total reviews run: {self.stats.total_count}")
        print(f"  Passed: {self.stats.ok_count}")
        print(f"  Failed: {self.stats.fail_count}")
        for status, label in (("skipped", "Skipped"), ("interrupted", "Interrupted")):
            n = sum(1 for r in self.stats.results if r.status == status)
            if n:
                print(f"  {label}: {n}")
        print(f"Total time: {fmt_duration(total)}")

        # Per-tool breakdown
        tool_summary = self.stats.tool_summary()
        if len(tool_summary) > 1 or any(s.model for s in self.tools):
            print()
            print("Per-tool stats:")
            for tool in sorted(tool_summary):
                counts = tool_summary[tool]
                parts = [f"{status}={count}" for status, count in sorted(counts.items())]
                print(f"  {tool:<20} {', '.join(parts)}")

        failures = sorted(
            (r for r in self.stats.results if r.status in ("fail", "timeout")),
            key=lambda r: r.review,
        )
        if failures:
            print()
            print("Failed reviews:")
            for r in failures:
                detail = "timeout" if r.status == "timeout" else f"exit {r.exit_code}"
                print(f"  - {r.review} ({r.tool.label()}) — {detail}")

    def list_reviews(self) -> None:
        print(f"Available reviews ({len(self.prompt_files)}):")
        for r, prompt_file in self.prompt_files.items():
            # The first "Your goal" line doubles as the description.
            desc = ""
            try:
                for line in prompt_file.read_text(encoding="utf-8").splitlines():
                    if line.startswith("Your goal"):
                        desc = re.sub(r"[\x00-\x1f\x7f]", "", line)
                        break
            except (OSError, UnicodeDecodeError):
                pass
            active = "✓" if r in self.reviews else "○"
            print(f"  {active} {r:<20}{self._origin(prompt_file)} {desc or '(no description)'}")

    def dry_run(self) -> None:
        print("DRY RUN — planned schedule for one loop:")
        order = list(self.reviews)
        random.shuffle(order)
        for r in order:
            print(f"  {r:<20}{self._origin(self.prompt_files[r])} → {self.pick_tool().label()}")
        print()
        print(f"Reviews per loop: {len(self.reviews)}")
        agents_str = ", ".join(s.label() for s in self.tools)
        print(
            f"Agents: {agents_str}  |  timeout: {fmt_duration(self.timeout_secs)}  |  "
            f"prompt-dir: {self.args.prompt_dir}"
        )
        missing = sorted(t for t in {s.tool for s in self.tools} if shutil.which(t) is None)
        if missing:
            print(f"Warning: not installed, would fail at runtime: {', '.join(missing)}")
        limit = str(self.args.max_loops) if self.args.max_loops else "infinite"
        print(f"Loop limit: {limit}")

    def run(self) -> None:
        signal.signal(signal.SIGINT, self.handle_signal)
        signal.signal(signal.SIGTERM, self.handle_signal)

        if self.args.list:
            self.list_reviews()
            return

        if self.args.dry_run:
            self.dry_run()
            return

        try:
            while True:
                order = list(self.reviews)
                random.shuffle(order)
                loop_start = time.monotonic()
                for r in order:
                    if self.stopping:
                        return
                    self.run_review(r)
                self.loop_count += 1
                loop_elapsed = time.monotonic() - loop_start
                print()
                log(
                    f"=== Loop {self.loop_count} complete in {fmt_duration(loop_elapsed)} "
                    f"({self.stats.total_count} reviews, {self.stats.fail_count} failures) ==="
                )
                print()

                if self.args.max_loops and self.loop_count >= self.args.max_loops:
                    break
        finally:
            self.print_stats()


def acquire_lock(path: Path) -> None:
    # The fd is deliberately left open (never closed) so the flock is held
    # for the lifetime of the process. O_NOFOLLOW rejects symlinks and
    # O_NONBLOCK keeps a planted FIFO from blocking the open forever.
    try:
        fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK, 0o644)
        if not os.path.isfile(path):
            sys.exit(f"Lock path is not a regular file: {path}")
        fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError:
        print(f"Another review-loop appears to be running (lock: {path})", file=sys.stderr)
        sys.exit(75)  # EX_TEMPFAIL
    except OSError as e:
        sys.exit(f"Cannot acquire lock file {path}: {e}")


def setup_log_tee(log_path: Path) -> None:
    check_tool("tee")
    try:
        with open(log_path, "a"):
            pass
    except OSError as e:
        sys.exit(f"Cannot write log file {log_path}: {e}")
    # New session so terminal Ctrl+C does not kill tee before the final
    # stats are written through it.
    tee = subprocess.Popen(
        ["tee", "-a", str(log_path)], stdin=subprocess.PIPE, start_new_session=True
    )
    assert tee.stdin is not None
    os.dup2(tee.stdin.fileno(), 1)
    os.dup2(tee.stdin.fileno(), 2)


def parse_args() -> argparse.Namespace:
    p = argparse.ArgumentParser(
        description="Run review prompts via claude/gemini/qwen/codex/grok/agy/cursor-agent.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Examples:\n"
            "  review-loop.py --agents claude\n"
            "  review-loop.py --agents mixed                 # all installed agents, default models\n"
            "  review-loop.py --agents claude,gemini,codex\n"
            "  review-loop.py --agents claude:opus-4-7,codex:gpt-5-codex\n"
            "  review-loop.py --agents mixed,claude:opus-4-7 # all + extra pinned model\n"
            "  review-loop.py --list                         # show available reviews\n"
        ),
    )
    p.add_argument(
        "--agents", "--models", dest="agents", type=parse_agents, default=None,
        help="comma-separated agent CLIs, optionally agent:model. "
             "Use 'mixed' (or 'random'/'all') as shorthand for every installed agent. "
             "Examples: 'claude', 'mixed', 'claude:opus-4-7,codex:gpt-5-codex'. "
             "Default: auto-detect installed agents. (--models is a deprecated alias.)",
    )
    p.add_argument("--dir", type=Path, default=None, help="cd into DIR before running")
    p.add_argument("--once", action="store_true", help="run a single loop and exit")
    p.add_argument("--max-loops", type=int, default=0, help="stop after N loops (0 = unlimited)")
    p.add_argument("--version", action="version", version="review-loop 1.0")
    p.add_argument(
        "--timeout", default="30m", type=parse_duration,
        help="per-review timeout (default 30m)",
    )
    p.add_argument("--log", type=Path, default=None, help="tee output to FILE")
    p.add_argument(
        "--prompt-dir", type=Path,
        default=Path(__file__).resolve().parent / "prompts",
        help="directory of *-review.md files (default: prompts/ next to this script)",
    )
    p.add_argument("--reviews", default="", help="comma-separated subset to run")
    p.add_argument("--exclude", default="", help="comma-separated reviews to skip")
    p.add_argument("--dry-run", action="store_true", help="print planned schedule and exit")
    p.add_argument("--list", action="store_true", help="list available reviews and exit")
    args = p.parse_args()
    if args.max_loops < 0:
        p.error("--max-loops must be >= 0")
    if args.once:
        if args.max_loops:
            p.error("--once conflicts with --max-loops")
        args.max_loops = 1
    return args


def autodetect_agents() -> list[ToolSpec]:
    found = installed_tools()
    if not found:
        usage_error(
            f"No supported agents found in PATH. Install one of: "
            f"{', '.join(sorted(VALID_TOOLS))}, or pass --agents explicitly."
        )
    return found


def main() -> None:
    args = parse_args()

    # Resolve against the invocation cwd, before any --dir chdir.
    args.prompt_dir = args.prompt_dir.resolve()

    if args.dir:
        try:
            os.chdir(args.dir)
        except OSError as e:
            usage_error(f"Cannot cd to {args.dir}: {e}")

    if not args.prompt_dir.is_dir():
        usage_error(f"Prompt directory not found: {args.prompt_dir}")

    if args.list:
        if args.agents is None:
            args.agents = installed_tools()
        Runner(args).run()
        return

    if args.agents is None:
        args.agents = autodetect_agents()
        log(f"Auto-detected agents: {','.join(s.label() for s in args.agents)}")

    if args.dry_run:
        Runner(args).run()
        return

    for tool in {s.tool for s in args.agents}:
        check_tool(tool)

    acquire_lock(Path.cwd() / ".review-loop.lock")

    if args.log:
        setup_log_tee(args.log)

    runner = Runner(args)
    try:
        runner.run()
    except BrokenPipeError:
        sys.exit(1)  # tee died; both output fds are gone, nothing to report to
    if runner.stopping:
        sys.exit(130)
    if any(r.status in ("fail", "timeout", "skipped") for r in runner.stats.results):
        sys.exit(1)


if __name__ == "__main__":
    main()
