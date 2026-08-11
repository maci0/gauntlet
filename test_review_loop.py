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
import shutil
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
        auto = sorted(rl.VALID_TOOLS - rl.OPT_IN_TOOLS)
        specs = rl.parse_agents("mixed")
        assert [s.tool for s in specs] == auto, "opt-in agents must not be auto-scheduled"
        assert all(s.model is None for s in specs)
        # a pinned model alongside 'mixed' is an additional distinct entry
        assert len(rl.parse_agents("mixed,claude:opus-4-7")) == len(auto) + 1
        # opt-in agents still work when named explicitly
        for tool in sorted(rl.OPT_IN_TOOLS):
            assert rl.parse_agents(tool) == [rl.ToolSpec(tool)]
        assert rl.OPT_IN_TOOLS < rl.VALID_TOOLS
        # with only opt-in agents installed, auto-detection must refuse
        rl.shutil.which = lambda name: f"/usr/bin/{name}" if name in rl.OPT_IN_TOOLS else None
        assert rl.installed_tools() == []
        raises(lambda: rl.parse_agents("mixed"))

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
        ("clanker", None): ["clanker", "run", "P"],
        ("opencode", None): ["opencode", "run", "--auto", "P"],
        ("opencode", "anthropic/claude"): ["opencode", "run", "--auto", "-m", "anthropic/claude", "P"],
        ("kimi", None): ["kimi", "-p", "P"],
        ("kimi", "k2"): ["kimi", "-m", "k2", "-p", "P"],
    }
    for (tool, model), want in expected.items():
        got = rl.build_cmd(rl.ToolSpec(tool, model), "P")
        assert got == want, f"{tool}:{model}\n got {got}\nwant {want}"

    # every supported agent must be covered here and must pass the prompt through
    assert {t for t, _ in expected} == rl.VALID_TOOLS
    for (tool, model) in expected:
        assert "P" in rl.build_cmd(rl.ToolSpec(tool, model), "P")

    raises(lambda: rl.build_cmd(rl.ToolSpec("nope"), "P"), ValueError)


def test_build_cmd_continue_session():
    assert rl.build_cmd(rl.ToolSpec("claude"), "P", continue_session=True) == \
        ["claude", "-c", "--dangerously-skip-permissions", "-p", "P"]
    assert rl.build_cmd(rl.ToolSpec("kimi"), "P", continue_session=True) == \
        ["kimi", "-c", "-p", "P"]
    assert rl.build_cmd(rl.ToolSpec("gemini"), "P", continue_session=True) == \
        ["gemini", "--resume", "latest", "-y", "-p", "P"]
    # subcommand tools take the flag after the subcommand, not next to the binary
    assert rl.build_cmd(rl.ToolSpec("opencode"), "P", continue_session=True) == \
        ["opencode", "run", "-c", "--auto", "P"]
    # unsupported tools silently start fresh
    assert rl.build_cmd(rl.ToolSpec("codex"), "P", continue_session=True) == \
        rl.build_cmd(rl.ToolSpec("codex"), "P")
    # every subcommand tool really does put its subcommand at index 1
    for tool, sub in rl.SUBCOMMAND_TOOLS.items():
        assert rl.build_cmd(rl.ToolSpec(tool), "P")[1] == sub
    # every CONTINUE_FLAGS key must be a valid tool
    assert set(rl.CONTINUE_FLAGS) <= rl.VALID_TOOLS


def test_continue_sessions_bootstrap_end_to_end():
    """First run per tool starts fresh; later runs carry the resume flag."""
    script = Path(__file__).resolve().parent / "review-loop.py"
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        bin_dir, proj, prompts = root / "bin", root / "proj", root / "prompts"
        for d in (bin_dir, proj, prompts):
            d.mkdir()
        _tree(prompts, {"stub-review.md": "You are a reviewer.\n\nYour goal is testing.\n"})
        argv_log = root / "argv.log"
        stub = bin_dir / "agy"
        stub.write_text(f'#!/bin/sh\necho "FIRST:$1" >> {argv_log}\nexit 0\n')
        stub.chmod(0o755)
        env = {**os.environ, "PATH": f"{bin_dir}:{os.environ['PATH']}"}
        p = subprocess.run(
            [sys.executable, str(script), "--max-loops", "2", "--agents", "agy",
             "--continue-sessions", "--prompt-dir", str(prompts), "--dir", str(proj),
             "--timeout", "30s"],
            env=env, capture_output=True, timeout=60, check=False,
        )
        assert p.returncode == 0, p.stderr
        lines = argv_log.read_text().splitlines()
        assert lines == [
            "FIRST:--dangerously-skip-permissions",  # fresh session
            "FIRST:-c",                              # resumed session
        ], lines


def test_bin_override():
    with tempfile.TemporaryDirectory() as td:
        fake = Path(td) / "my-claude"
        fake.write_text("#!/bin/sh\nexit 0\n")
        fake.chmod(0o755)

        tool, resolved = rl.parse_bin(f"claude={fake}")
        assert (tool, resolved) == ("claude", str(fake))
        # only the executable changes; every other argument stays put
        assert rl.build_cmd(rl.ToolSpec("claude", "opus"), "P", binary=resolved) == \
            [str(fake), "--dangerously-skip-permissions", "--model", "opus", "-p", "P"]
        # overrides survive subcommand agents and session flags
        assert rl.build_cmd(rl.ToolSpec("opencode"), "P", continue_session=True,
                            binary=resolved)[:3] == [str(fake), "run", "-c"]
        # a bare name is looked up on PATH
        assert rl.parse_bin("claude=sh")[1] == shutil.which("sh")
        # shells leave ~ and $VAR unexpanded after '=', so the parser expands
        home_rel = os.path.relpath(fake, Path.home())
        if not home_rel.startswith(".."):
            assert rl.parse_bin(f"claude=~/{home_rel}")[1] == str(fake)
        os.environ["RL_TEST_BIN"] = str(fake.parent)
        try:
            assert rl.parse_bin(f"claude=$RL_TEST_BIN/{fake.name}")[1] == str(fake)
        finally:
            del os.environ["RL_TEST_BIN"]

    raises(lambda: rl.parse_bin("claude"))                    # no '='
    raises(lambda: rl.parse_bin("=/bin/sh"))                  # no tool
    raises(lambda: rl.parse_bin("claude="))                   # no path
    raises(lambda: rl.parse_bin("nosuchagent=/bin/sh"))       # unknown agent
    raises(lambda: rl.parse_bin("claude=/nonexistent/nope"))  # not executable


def test_bin_override_end_to_end():
    """A named agent runs from --bin even though PATH holds a different one."""
    script = Path(__file__).resolve().parent / "review-loop.py"
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        bin_dir, proj, prompts = root / "bin", root / "proj", root / "prompts"
        for d in (bin_dir, proj, prompts):
            d.mkdir()
        _tree(prompts, {"stub-review.md": "Role.\n\nYour goal is testing.\n"})
        marker = root / "ran.txt"
        # PATH copy fails; the override succeeds, so only the override can pass
        (bin_dir / "agy").write_text("#!/bin/sh\nexit 9\n")
        (bin_dir / "agy").chmod(0o755)
        custom = root / "agy-custom"
        custom.write_text(f'#!/bin/sh\necho ran > {marker}\nexit 0\n')
        custom.chmod(0o755)

        p = subprocess.run(
            [sys.executable, str(script), "--once", "--agents", "agy",
             "--bin", f"agy={custom}", "--prompt-dir", str(prompts),
             "--dir", str(proj), "--timeout", "30s"],
            env={**os.environ, "PATH": f"{bin_dir}:{os.environ['PATH']}"},
            capture_output=True, text=True, timeout=60, check=False,
        )
        assert p.returncode == 0, p.stdout + p.stderr
        assert marker.exists(), "the --bin executable should have run"


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
        for bad in ("sym.lock", "fifo.lock"):
            if bad == "fifo.lock":
                os.mkfifo(root / bad)
            try:
                rl.acquire_lock(root / bad)
                raise AssertionError("expected SystemExit")
            except SystemExit as e:
                assert e.code == 2, f"{bad}: usage error expected, got {e.code}"
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
    """Exit codes AND status classification via a stub agent."""
    script = Path(__file__).resolve().parent / "review-loop.py"
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        bin_dir, proj, prompts = root / "bin", root / "proj", root / "prompts"
        for d in (bin_dir, proj, prompts):
            d.mkdir()
        _tree(prompts, {"stub-review.md": "You are a reviewer.\n\nYour goal is testing.\n"})
        stub = bin_dir / "agy"
        env = {**os.environ, "PATH": f"{bin_dir}:{os.environ['PATH']}"}

        def run(stub_body: str, timeout: str = "30s", log: str | None = None,
                extra: list[str] | None = None):
            stub.write_text(f"#!/bin/sh\n{stub_body}\n")
            stub.chmod(0o755)
            args = [sys.executable, str(script), "--once", "--agents", "agy",
                    "--prompt-dir", str(prompts), "--dir", str(proj), "--timeout", timeout]
            if log:
                args += ["--log", log]
            args += extra or []
            p = subprocess.run(args, env=env, capture_output=True, text=True,
                               timeout=60, check=False)
            return p.returncode, p.stdout + p.stderr

        rc, out = run("exit 0")
        assert rc == 0 and "Done: stub-review" in out, out
        rc, out = run("echo AGENT-NOISE; exit 0")
        assert "AGENT-NOISE" in out, "agent output is inherited by default"
        rc, out = run("echo AGENT-NOISE; exit 0", extra=["--quiet-agents"])
        assert rc == 0 and "AGENT-NOISE" not in out and "Done: stub-review" in out, out
        rc, out = run("exit 3")
        assert rc == 1 and "FAILED: stub-review" in out, out
        rc, out = run("sleep 30", timeout="1s")
        assert rc == 1 and "TIMEOUT: stub-review" in out, "timeout must classify as TIMEOUT, not FAILED"

        # --log tee path: log lines must land in the file
        log_file = root / "run.log"
        rc, _ = run("exit 0", log=str(log_file))
        assert rc == 0 and "Done: stub-review" in log_file.read_text()


def test_agents_never_inherit_stdin():
    """An agent with the terminal can disable ISIG and break Ctrl+C for everyone."""
    with tempfile.TemporaryDirectory() as td:
        prompts = Path(td) / "prompts"
        _tree(prompts, {"stub-review.md": "Role.\n\nYour goal is testing.\n"})
        args = argparse.Namespace(
            agents=[rl.ToolSpec("claude")], prompt_dir=prompts, reviews="", exclude="",
            timeout=30, quiet_agents=False, continue_sessions=False, bin={},
        )
        runner = rl.Runner(args)
        captured = {}

        class FakeProc:
            pid = 4242
            returncode = 0

            def wait(self, timeout=None):
                return 0

            def poll(self):
                return 0

        def fake_popen(cmd, **kwargs):
            captured.update(kwargs)
            return FakeProc()

        real_popen = rl.subprocess.Popen
        rl.subprocess.Popen = fake_popen
        try:
            runner.run_review("stub-review")
        finally:
            rl.subprocess.Popen = real_popen
        assert captured.get("stdin") is rl.subprocess.DEVNULL, captured
        assert captured.get("start_new_session") is True


def test_run_review_interrupt_exits_130():
    script = Path(__file__).resolve().parent / "review-loop.py"
    import signal as _signal
    with tempfile.TemporaryDirectory() as td:
        root = Path(td)
        bin_dir, proj, prompts = root / "bin", root / "proj", root / "prompts"
        for d in (bin_dir, proj, prompts):
            d.mkdir()
        _tree(prompts, {"stub-review.md": "You are a reviewer.\n\nYour goal is testing.\n"})
        stub = bin_dir / "agy"
        stub.write_text("#!/bin/sh\nsleep 30\n")
        stub.chmod(0o755)
        env = {**os.environ, "PATH": f"{bin_dir}:{os.environ['PATH']}"}
        p = subprocess.Popen(
            [sys.executable, str(script), "--once", "--agents", "agy",
             "--prompt-dir", str(prompts), "--dir", str(proj), "--timeout", "30s"],
            env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
        )
        time.sleep(3)  # let it launch the stub
        p.send_signal(_signal.SIGINT)
        out, _ = p.communicate(timeout=30)
        assert p.returncode == 130, (p.returncode, out)
        assert "Interrupted: stub-review" in out, out


def test_doctor_recommended_tools_are_real_entries():
    listed = {t for tools in rl.REVIEW_TOOLS.values() for t in tools}
    assert rl.RECOMMENDED_TOOLS <= listed, rl.RECOMMENDED_TOOLS - listed


def test_review_sets_reference_real_prompts():
    """A renamed or removed prompt must not linger in a set."""
    bundled = {f.stem for f in (Path(__file__).parent / "prompts").glob("*-review.md")}
    for name, members in rl.REVIEW_SETS.items():
        assert members, f"{name} is empty"
        assert len(set(members)) == len(members), f"{name} has duplicates"
        missing = set(members) - bundled
        assert not missing, f"{name} references missing prompts: {missing}"
    assert not set(rl.REVIEW_SETS) & set(rl.DYNAMIC_SETS), "dynamic names are reserved"
    assert not bundled & set(rl.REVIEW_SETS), "a set must not shadow a review name"
    assert not bundled & set(rl.DYNAMIC_SETS)


def test_project_set_selects_only_project_prompts():
    script = Path(__file__).resolve().parent / "review-loop.py"
    with tempfile.TemporaryDirectory() as td:
        root = Path(td).resolve()
        bundled, proj = root / "prompts", root / "proj"
        _tree(bundled, {"code-review.md": "x", "sec-review.md": "x"})
        _tree(proj, {"house-review.md": "x", "docs/legacy-review.md": "x"})

        def scheduled(spec: str) -> set[str]:
            p = subprocess.run(
                [sys.executable, str(script), "--dry-run", "--agents", "claude",
                 "--prompt-dir", str(bundled), "--dir", str(proj), "--reviews", spec],
                capture_output=True, text=True, timeout=60, check=False,
            )
            assert p.returncode == 0, p.stderr
            return {ln.split()[0] for ln in p.stdout.splitlines() if "→" in ln}

        assert scheduled("project") == {"house-review", "legacy-review"}
        assert scheduled("all") == {"code-review", "sec-review", "house-review", "legacy-review"}

        # a set that matches nothing fails clearly instead of running everything
        empty = subprocess.run(
            [sys.executable, str(script), "--dry-run", "--agents", "claude",
             "--prompt-dir", str(bundled), "--dir", str(bundled), "--reviews", "project"],
            capture_output=True, text=True, timeout=60, check=False,
        )
        assert empty.returncode == 2 and "matched no available reviews" in empty.stderr


def test_reviews_flag_expands_sets():
    script = Path(__file__).resolve().parent / "review-loop.py"

    def scheduled(spec: str, flag: str = "--reviews") -> set[str]:
        p = subprocess.run(
            [sys.executable, str(script), "--dry-run", "--agents", "claude", flag, spec],
            capture_output=True, text=True, timeout=60, check=False,
        )
        assert p.returncode == 0, p.stderr
        return {ln.split()[0] for ln in p.stdout.splitlines() if "→" in ln}

    assert scheduled("quick") == set(rl.REVIEW_SETS["quick"])
    # sets compose with plain names, and duplicates collapse
    assert scheduled("quick,llm-review,code-review") == \
        set(rl.REVIEW_SETS["quick"]) | {"llm-review"}
    # excluding a set removes exactly its members
    every = scheduled("all")
    assert scheduled("all", "--reviews") == every
    assert scheduled("frontend", "--exclude") == every - set(rl.REVIEW_SETS["frontend"])

    bad = subprocess.run(
        [sys.executable, str(script), "--dry-run", "--agents", "claude",
         "--reviews", "nosuchset"],
        capture_output=True, text=True, timeout=60, check=False,
    )
    assert bad.returncode == 2 and "Sets:" in bad.stderr, bad.stderr


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


def test_compose_prompt_prompt_review_exception():
    body = "Role.\n\nInstructions:\n- x\n"
    normal = rl.compose_prompt(body, 60, "sec-review")
    meta = rl.compose_prompt(body, 60, "prompt-review")
    assert "Exception for this review only" not in normal
    assert "you may MODIFY existing" in meta
    assert "Creating or deleting them remains forbidden" in meta


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
