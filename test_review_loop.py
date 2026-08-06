#!/usr/bin/env python3
# Copyright (C) 2026 Marcel W. Wysocki
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Tests for review-loop.py. Run: ./test_review_loop.py (or pytest)."""

from __future__ import annotations

import argparse
import contextlib
import importlib.util
import io
import os
import random
import re
import subprocess
import sys
import tempfile
import time
from pathlib import Path


def _load_runner():
    path = Path(__file__).resolve().parent / "review-loop.py"
    spec = importlib.util.spec_from_file_location("review_loop", path)
    mod = importlib.util.module_from_spec(spec)
    sys.modules["review_loop"] = mod  # dataclasses resolve types via sys.modules
    spec.loader.exec_module(mod)
    return mod


rl = _load_runner()


def raises(fn, exc=argparse.ArgumentTypeError) -> None:
    try:
        fn()
    except exc:
        return
    raise AssertionError(f"expected {exc.__name__} from {fn}")


def _tree(root: Path, files: dict[str, str]) -> None:
    for rel, text in files.items():
        p = root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(text)


@contextlib.contextmanager
def _cwd(path: Path):
    old = Path.cwd()
    os.chdir(path)
    try:
        yield
    finally:
        os.chdir(old)


def test_parse_duration_units():
    assert rl.parse_duration("90") == 90
    assert rl.parse_duration("90s") == 90
    assert rl.parse_duration("30m") == 1800
    assert rl.parse_duration("1h") == 3600
    assert rl.parse_duration("2d") == 172800


def test_parse_duration_rejects_bad_input():
    for bad in ("0", "0s", "0m", "", "1.5h", "5x", "-5", "m", "10 m", "1,2", "30M", " 30m"):
        raises(lambda b=bad: rl.parse_duration(b))


def test_fmt_duration():
    assert rl.fmt_duration(90) == "1m30s"
    assert rl.fmt_duration(1800) == "30m00s"
    assert rl.fmt_duration(3600) == "1h00m"
    assert rl.fmt_duration(172800) == "48h00m"


def test_parse_agents_basic():
    assert rl.parse_agents("claude") == [rl.ToolSpec("claude")]
    assert rl.parse_agents("claude:opus-4-7") == [rl.ToolSpec("claude", "opus-4-7")]
    # order preserved, whitespace and empty entries tolerated
    assert [s.tool for s in rl.parse_agents(" codex , ,claude ")] == ["codex", "claude"]


def test_parse_agents_dedups():
    assert rl.parse_agents("claude,claude") == [rl.ToolSpec("claude")]
    # same tool with different models stays distinct
    assert len(rl.parse_agents("claude:a,claude:b")) == 2


def test_parse_agents_rejects_bad_input():
    raises(lambda: rl.parse_agents(""))
    raises(lambda: rl.parse_agents(","))
    raises(lambda: rl.parse_agents("nosuchtool"))
    raises(lambda: rl.parse_agents("claude,nosuchtool"))
    for tool in rl.NO_MODEL_TOOLS:
        raises(lambda t=tool: rl.parse_agents(f"{t}:some-model"))


def test_parse_agents_mixed_expands_to_installed():
    orig = rl.shutil.which
    try:
        rl.shutil.which = lambda name: f"/usr/bin/{name}"
        specs = rl.parse_agents("mixed")
        assert [s.tool for s in specs] == sorted(rl.VALID_TOOLS)
        assert all(s.model is None for s in specs)
        # a pinned model alongside 'mixed' is an additional distinct entry
        assert len(rl.parse_agents("mixed,claude:opus-4-7")) == len(rl.VALID_TOOLS) + 1

        rl.shutil.which = lambda name: "/usr/bin/claude" if name == "claude" else None
        assert rl.parse_agents("all") == [rl.ToolSpec("claude")]

        rl.shutil.which = lambda name: None
        raises(lambda: rl.parse_agents("random"))
    finally:
        rl.shutil.which = orig


def test_build_cmd_exact_argv():
    """Every agent's argv, including the permission-bypass flag it must carry."""
    expected = {
        ("claude", None): ["claude", "--dangerously-skip-permissions", "-p", "P"],
        ("claude", "opus"): ["claude", "--dangerously-skip-permissions", "--model", "opus", "-p", "P"],
        ("gemini", None): ["gemini", "-y", "-p", "P"],
        ("gemini", "g-2"): ["gemini", "-y", "-m", "g-2", "-p", "P"],
        ("qwen", None): ["qwen", "-y", "-p", "P"],
        ("qwen", "q-3"): ["qwen", "-y", "-m", "q-3", "-p", "P"],
        ("codex", None): ["codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "P"],
        ("codex", "gpt"): ["codex", "exec", "--dangerously-bypass-approvals-and-sandbox", "-m", "gpt", "P"],
        ("grok", None): ["grok", "--permission-mode", "bypassPermissions", "-p", "P"],
        ("grok", "g-4"): ["grok", "--permission-mode", "bypassPermissions", "-m", "g-4", "-p", "P"],
        ("agy", None): ["agy", "--dangerously-skip-permissions", "-p", "P"],
        ("cursor-agent", None): ["cursor-agent", "--print", "-f", "P"],
        ("cursor-agent", "c-1"): ["cursor-agent", "--print", "-f", "--model", "c-1", "P"],
    }
    for (tool, model), want in expected.items():
        got = rl.build_cmd(rl.ToolSpec(tool, model), "P")
        assert got == want, f"{tool}:{model}\n got {got}\nwant {want}"

    # every supported agent must be covered here and must pass the prompt through
    assert {t for t, _ in expected} == rl.VALID_TOOLS
    for (tool, model) in expected:
        assert "P" in rl.build_cmd(rl.ToolSpec(tool, model), "P")

    raises(lambda: rl.build_cmd(rl.ToolSpec("nope"), "P"), ValueError)


def test_build_cmd_prompt_is_never_flag_like():
    """The prompt is passed as one argv element, so its content cannot inject flags."""
    hostile = "--dangerously-skip-permissions\n--help"
    for tool in sorted(rl.VALID_TOOLS):
        cmd = rl.build_cmd(rl.ToolSpec(tool), hostile)
        assert cmd.count(hostile) == 1
        assert cmd[0] == tool


def test_discover_reviews_skips_prompt_dir_inside_cwd():
    """Self-review case: the bundled dir lives under the project being reviewed."""
    with tempfile.TemporaryDirectory() as td:
        proj = Path(td).resolve()
        bundled = proj / "prompts"
        _tree(bundled, {"code-review.md": "x"})
        _tree(proj, {"extra-review.md": "x"})
        with _cwd(proj):
            found = rl.discover_reviews(bundled)
        assert found["code-review"] == bundled / "code-review.md"
        assert found["extra-review"] == proj / "extra-review.md"
        assert len(found) == 2, "bundled prompts must not be discovered twice"


def test_discover_reviews_merges_bundled_and_project():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td).resolve()
        bundled, proj = root / "prompts", root / "proj"
        _tree(bundled, {"code-review.md": "x", "sec-review.md": "bundled"})
        _tree(proj, {
            "docs/custom-review.md": "x",
            "sec-review.md": "project",              # overrides the bundled one
            "node_modules/junk/evil-review.md": "x",  # inside a skipped dir
            "notes.md": "x",                          # not a review file
        })
        err = io.StringIO()
        with _cwd(proj), contextlib.redirect_stderr(err):
            found = rl.discover_reviews(bundled)

        assert set(found) == {"code-review", "sec-review", "custom-review"}
        assert found["sec-review"] == proj / "sec-review.md"
        assert "overrides the bundled one" in err.getvalue()
        assert found["code-review"] == bundled / "code-review.md"
        assert list(found) == sorted(found), "listing must be sorted"


def test_discover_reviews_ignores_symlinks_and_control_chars():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td).resolve()
        bundled, proj = root / "prompts", root / "proj"
        outside = root / "outside-review.md"
        outside.write_text("out of tree")
        _tree(bundled, {"code-review.md": "x"})
        _tree(proj, {"real-review.md": "x"})
        (proj / "link-review.md").symlink_to(outside)
        (proj / "spoof\x01-review.md").write_text("x")

        with _cwd(proj):
            found = rl.discover_reviews(bundled)

        assert "link-review" not in found, "symlinks must never be followed"
        assert not any("\x01" in name for name in found)
        assert set(found) == {"code-review", "real-review"}


def test_discover_reviews_duplicate_project_prompts_warn_first_wins():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td).resolve()
        bundled, proj = root / "prompts", root / "proj"
        bundled.mkdir(parents=True)
        _tree(proj, {"a/dup-review.md": "first", "b/dup-review.md": "second"})

        err = io.StringIO()
        with _cwd(proj), contextlib.redirect_stderr(err):
            found = rl.discover_reviews(bundled)

        assert found["dup-review"] == proj / "a" / "dup-review.md"
        assert "duplicate project prompt" in err.getvalue()


def test_doctor_plain_when_not_a_tty():
    out = io.StringIO()  # StringIO is not a tty, so output must stay unstyled
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
        rl.doctor()
    text = out.getvalue()
    assert "\033" not in text, "ANSI escapes must not reach a non-terminal"
    assert "Agent CLIs" in text and "Per-review helpers" in text


def test_doctor_colors_and_wraps_when_forced():
    real_env = os.environ.get("TERM")
    real_no_color = os.environ.pop("NO_COLOR", None)
    out = io.StringIO()
    out.isatty = lambda: True  # force the color path
    os.environ["TERM"] = "xterm"
    try:
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
            rl.doctor()
        text = out.getvalue()
        assert "\033[" in text, "expected ANSI styling"
        # no line of visible text should exceed the terminal width used for wrapping
        width = max(rl.shutil.get_terminal_size((100, 24)).columns, 60)
        for line in text.splitlines():
            visible = re.sub(r"\033\[[0-9;]*m", "", line)
            assert len(visible) <= width + 1, f"line exceeds width {width}: {visible!r}"
    finally:
        if real_env is None:
            os.environ.pop("TERM", None)
        else:
            os.environ["TERM"] = real_env
        if real_no_color is not None:
            os.environ["NO_COLOR"] = real_no_color


def test_doctor_counts_unique_binaries():
    out = io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(io.StringIO()):
        rl.doctor()
    m = re.search(r"recommended (\d+)/(\d+)", out.getvalue())
    assert m, out.getvalue()
    assert int(m.group(2)) == len(rl.RECOMMENDED_TOOLS), "tools shared by reviews must count once"


def test_sanitize_strips_control_and_bidi():
    assert rl.sanitize("a\x1b[31mb") == "a[31mb"
    assert rl.sanitize("a\x9bb") == "ab"           # C1 CSI
    assert rl.sanitize("a\u202eb") == "ab"         # bidi override
    assert rl.sanitize("plain text 123") == "plain text 123"


def test_acquire_lock_rejects_symlink_fifo_and_contention():
    import fcntl
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        (root / "target").write_text("")
        (root / "sym.lock").symlink_to(root / "target")
        raises(lambda: rl.acquire_lock(root / "sym.lock"), SystemExit)
        os.mkfifo(root / "fifo.lock")
        raises(lambda: rl.acquire_lock(root / "fifo.lock"), SystemExit)
        # contention: hold the flock ourselves, expect exit 75
        held = os.open(root / "busy.lock", os.O_WRONLY | os.O_CREAT, 0o644)
        try:
            fcntl.flock(held, fcntl.LOCK_EX | fcntl.LOCK_NB)
            try:
                rl.acquire_lock(root / "busy.lock")
                raise AssertionError("expected SystemExit")
            except SystemExit as e:
                assert e.code == 75, e.code
        finally:
            os.close(held)


def test_run_review_status_machine_end_to_end():
    """Exit-code contract via a stub agent: ok, fail, and timeout paths."""
    script = Path(__file__).resolve().parent / "review-loop.py"
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        bin_dir, proj, prompts = root / "bin", root / "proj", root / "prompts"
        bin_dir.mkdir(), proj.mkdir(), prompts.mkdir()
        _tree(prompts, {"stub-review.md": "You are a reviewer.\n\nYour goal is testing.\n"})
        stub = bin_dir / "agy"
        env = {**os.environ, "PATH": f"{bin_dir}:{os.environ['PATH']}"}

        def run(stub_body: str, timeout: str = "30s") -> int:
            stub.write_text(f"#!/bin/sh\n{stub_body}\n")
            stub.chmod(0o755)
            return subprocess.run(
                [sys.executable, str(script), "--once", "--agents", "agy",
                 "--prompt-dir", str(prompts), "--dir", str(proj), "--timeout", timeout],
                env=env, capture_output=True, timeout=60, check=False,
            ).returncode

        assert run("exit 0") == 0, "passing review must exit 0"
        assert run("exit 3") == 1, "failing review must exit 1"
        assert run("sleep 30", timeout="1s") == 1, "timed-out review must exit 1"


def test_doctor_recommended_tools_are_real_entries():
    listed = {t for tools in rl.REVIEW_TOOLS.values() for t in tools}
    assert rl.RECOMMENDED_TOOLS <= listed, rl.RECOMMENDED_TOOLS - listed


def test_doctor_tables_cover_every_bundled_prompt():
    """Adding a prompt without registering it in doctor's tables is drift."""
    bundled = {f.stem for f in (Path(__file__).parent / "prompts").glob("*-review.md")}
    covered = set(rl.REVIEW_TOOLS) | set(rl.REVIEWS_WITHOUT_TOOLS)
    assert bundled == covered, (
        f"unregistered: {bundled - covered}, stale: {covered - bundled}"
    )
    assert not set(rl.REVIEW_TOOLS) & set(rl.REVIEWS_WITHOUT_TOOLS)


def test_strip_report_sections_on_real_prompt():
    text = (Path(__file__).parent / "prompts" / "sec-review.md").read_text()
    stripped = rl.strip_report_sections(text)
    assert "For each finding include:" not in stripped
    assert "Output format:" not in stripped
    assert "## Executive Summary" not in stripped
    assert "Important:" in stripped, "the Important block must survive"
    assert "Instructions:" in stripped
    assert len(stripped) < len(text) * 0.85, "expected a substantial cut"
    # every bundled prompt must strip cleanly and keep its Important block
    for f in (Path(__file__).parent / "prompts").glob("*-review.md"):
        s = rl.strip_report_sections(f.read_text())
        assert "Output format:" not in s, f.name
        assert "Important:" in s, f.name


def test_prompt_suffix_renders():
    rendered = rl.PROMPT_SUFFIX.format(timeout="30m00s")
    assert "30m00s wall clock" in rendered
    assert "{" not in rendered, "unrendered placeholder or stray brace"
    assert "RESULT:" in rendered


def test_strip_report_sections_fails_open_without_important():
    # marker with no Important block: keep everything rather than eat the tail
    text = "keep this\nOutput format:\nand this too\nno important line"
    assert rl.strip_report_sections(text) == text
    # decorated/indented markers never trigger stripping
    text2 = "a\n  Output format:\n- Output format:\nb"
    assert rl.strip_report_sections(text2) == text2


def test_compose_prompt_order_and_content():
    body = "Role line.\n\nInstructions:\n- do X\n\nOutput format:\n## Report\nstuff\n\nImportant:\n- rule"
    prompt = rl.compose_prompt(body, 1800)
    assert prompt.startswith(rl.PROMPT_HEADER)
    assert prompt.rstrip().endswith("'RESULT: changed=N' | 'RESULT: no-changes' | 'RESULT: skipped (reason)'.")
    assert "## Report" not in prompt, "report section must be stripped"
    assert "- rule" in prompt, "Important block must survive"
    assert "30m00s wall clock" in prompt
    assert prompt.index(rl.PROMPT_HEADER) < prompt.index("- do X") < prompt.index("RESULT:")


def test_read_no_follow_rejects_fifo_and_symlink_fast():
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        os.mkfifo(root / "pipe-review.md")
        (root / "real.md").write_text("x")
        (root / "link-review.md").symlink_to(root / "real.md")
        t0 = time.monotonic()
        raises(lambda: rl.read_no_follow(root / "pipe-review.md"), OSError)
        raises(lambda: rl.read_no_follow(root / "link-review.md"), OSError)
        assert time.monotonic() - t0 < 2, "must fail fast, not block"


def test_fuzz_parsers_never_crash():
    """Parsers take user input: they may reject, but must not raise anything else."""
    rng = random.Random(20240805)
    alphabet = "claudexgemini:,-_ 0123456789smhd|*\t\n\x00é/\\'\"mixed"
    for _ in range(3000):
        s = "".join(rng.choice(alphabet) for _ in range(rng.randint(0, 24)))
        for parse in (rl.parse_duration, rl.parse_agents):
            try:
                parse(s)
            except argparse.ArgumentTypeError:
                pass
        # text processors must never raise on arbitrary input
        rl.sanitize(s)
        rl.strip_report_sections(s)


def main() -> int:
    tests = [(n, f) for n, f in sorted(globals().items())
             if n.startswith("test_") and callable(f)]
    failed = 0
    for name, fn in tests:
        try:
            fn()
            print(f"ok   {name}")
        except Exception as e:  # noqa: BLE001 - test runner reports everything
            failed += 1
            print(f"FAIL {name}: {e!r}")
    print(f"\n{len(tests) - failed}/{len(tests)} passed")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
