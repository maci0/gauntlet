#!/usr/bin/env python3
"""Tests for review-loop.py. Run: ./test_review_loop.py (or pytest)."""

from __future__ import annotations

import argparse
import contextlib
import importlib.util
import io
import os
import random
import sys
import tempfile
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
        with _cwd(proj):
            found = rl.discover_reviews(bundled)

        assert set(found) == {"code-review", "sec-review", "custom-review"}
        assert found["sec-review"] == proj / "sec-review.md"
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
