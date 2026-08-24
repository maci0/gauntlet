#!/usr/bin/env python3
# Copyright (C) 2026 Marcel W. Wysocki
# SPDX-License-Identifier: AGPL-3.0-or-later
"""Experimental curses TUI for gauntlet. stdlib only, no deps.

Design: cockpit, not a report (tmog). Bright live data, dim chrome.
Vertical panel stack: header, reviews, stats, agent output, footer.
"""

from __future__ import annotations

import collections
import curses
import threading
import time
from dataclasses import dataclass, field

# Color pair indices
C_DIM = 1
C_OK = 2
C_FAIL = 3
C_WARN = 4
C_ACCENT = 5
C_HEADER = 6
C_BAR_FILL = 7
C_BAR_EMPTY = 8
C_RUNNING = 9
C_KEY = 10

# Rounded box drawing
TL, TR, BL, BR, H, V = "╭", "╮", "╰", "╯", "─", "│"

STATUS_ICON = {
    "pending": "·", "running": "▸", "ok": "✓",
    "fail": "✗", "timeout": "⧖", "skipped": "–",
    "interrupted": "✗",
}
STATUS_COLOR = {
    "pending": C_DIM, "running": C_RUNNING, "ok": C_OK,
    "fail": C_FAIL, "timeout": C_WARN, "skipped": C_DIM,
    "interrupted": C_WARN,
}


@dataclass
class ReviewSlot:
    name: str
    agent: str = ""
    status: str = "pending"
    start_time: float = 0.0
    elapsed: float = 0.0
    tokens: int = 0


@dataclass
class TuiState:
    version: str = ""
    loop_count: int = 0
    max_loops: int = 0
    runtime_budget: float = 0.0
    script_start: float = 0.0
    ok: int = 0
    fail: int = 0
    timeout: int = 0
    skip: int = 0
    tokens: int = 0
    reviews: list[ReviewSlot] = field(default_factory=list)
    log_lines: collections.deque = field(default_factory=lambda: collections.deque(maxlen=2000))
    stopped: bool = False
    scroll: int = 0
    lock: threading.Lock = field(default_factory=threading.Lock)


def _init_colors() -> None:
    curses.start_color()
    curses.use_default_colors()
    pairs = [
        (C_DIM, 242, -1), (C_OK, 35, -1), (C_FAIL, 196, -1),
        (C_WARN, 214, -1), (C_ACCENT, 75, -1), (C_HEADER, 255, 236),
        (C_BAR_FILL, 35, -1), (C_BAR_EMPTY, 240, -1),
        (C_RUNNING, 81, -1), (C_KEY, 141, -1),
    ]
    for idx, fg, bg in pairs:
        curses.init_pair(idx, fg, bg)


def _dur(secs: float) -> str:
    if secs < 0:
        return "0s"
    h, rem = divmod(int(secs), 3600)
    m, s = divmod(rem, 60)
    if h:
        return f"{h}h{m:02d}m"
    if m:
        return f"{m}m{s:02d}s"
    return f"{s}s"


def _put(win: curses.window, y: int, x: int, text: str, attr: int = 0) -> None:
    h, w = win.getmaxyx()
    if y < 0 or y >= h or x >= w:
        return
    try:
        win.addnstr(y, x, text, max(0, w - x), attr)
    except curses.error:
        pass


def _box(win: curses.window, y: int, x: int, bh: int, bw: int, title: str = "") -> None:
    attr = curses.color_pair(C_DIM)
    top = title + " " + H * max(0, bw - 2 - len(title) - 1) if title else H * (bw - 2)
    _put(win, y, x, TL + top + TR, attr)
    for row in range(1, bh - 1):
        _put(win, y + row, x, V, attr)
        _put(win, y + row, x + bw - 1, V, attr)
    _put(win, y + bh - 1, x, BL + H * (bw - 2) + BR, attr)


def _gauge(win: curses.window, y: int, x: int, width: int,
           frac: float, label: str = "") -> None:
    lw = len(label) + 1 if label else 0
    segs = max(1, width - lw)
    filled = max(0, min(int(frac * segs), segs))
    if label:
        _put(win, y, x, label + " ", curses.color_pair(C_DIM))
    _put(win, y, x + lw, "━" * filled, curses.color_pair(C_BAR_FILL))
    _put(win, y, x + lw + filled, "─" * (segs - filled), curses.color_pair(C_BAR_EMPTY))


def _render(scr: curses.window, st: TuiState) -> None:
    scr.erase()
    h, w = scr.getmaxyx()
    if h < 8 or w < 40:
        _put(scr, 0, 0, "Terminal too small", curses.color_pair(C_WARN))
        scr.noutrefresh()
        return

    now = time.monotonic()
    with st.lock:
        revs = list(st.reviews)
        lines = list(st.log_lines)
        loop, ml = st.loop_count, st.max_loops
        ok, fail, tout, skip = st.ok, st.fail, st.timeout, st.skip
        tok, budget, start = st.tokens, st.runtime_budget, st.script_start
        stopped, scroll = st.stopped, st.scroll

    elapsed = now - start if start else 0.0

    # Layout heights
    review_h = min(len(revs) + 2, max(4, h // 3))
    stats_h = 4
    footer_h = 1
    output_h = max(3, h - 1 - review_h - stats_h - footer_h)

    # -- Header bar --
    hdr_l = f" gauntlet {st.version}"
    loop_s = f"loop {loop}" + (f"/{ml}" if ml else "")
    rt_s = f"runtime {_dur(elapsed)}" + (f"/{_dur(budget)}" if budget else "")
    flag = "DONE" if stopped else "RUNNING"
    hdr_r = f"{loop_s}  {rt_s}  {flag} "
    _put(scr, 0, 0, " " * w, curses.color_pair(C_HEADER) | curses.A_BOLD)
    _put(scr, 0, 0, hdr_l, curses.color_pair(C_HEADER) | curses.A_BOLD)
    _put(scr, 0, max(0, w - len(hdr_r)), hdr_r, curses.color_pair(C_HEADER))

    # -- Reviews panel --
    ry = 1
    _box(scr, ry, 0, review_h, w, "Reviews")
    name_w = min(25, w // 3)
    agent_w = min(20, w // 4)
    for i, rev in enumerate(revs):
        if i >= review_h - 2:
            break
        row = ry + 1 + i
        cp = curses.color_pair(STATUS_COLOR.get(rev.status, C_DIM))
        icon = STATUS_ICON.get(rev.status, " ")
        _put(scr, row, 2, icon, cp | curses.A_BOLD)
        _put(scr, row, 4, rev.name[:name_w].ljust(name_w), cp)
        ac = 5 + name_w
        _put(scr, row, ac, (rev.agent[:agent_w] if rev.agent else "").ljust(agent_w),
             curses.color_pair(C_DIM))
        dc = ac + agent_w + 1
        if rev.status == "running":
            _put(scr, row, dc, _dur(now - rev.start_time), curses.color_pair(C_RUNNING))
        elif rev.status == "ok":
            d = _dur(rev.elapsed)
            if rev.tokens:
                d += f"  {rev.tokens:,} tok"
            _put(scr, row, dc, d, curses.color_pair(C_OK))
        elif rev.status in ("fail", "timeout"):
            d = rev.status if not rev.elapsed else _dur(rev.elapsed)
            _put(scr, row, dc, d, cp)
        else:
            _put(scr, row, dc, rev.status, curses.color_pair(C_DIM))

    # -- Stats --
    sy = ry + review_h
    parts = [
        ("pass ", C_OK, str(ok)), ("  fail ", C_FAIL, str(fail)),
        ("  timeout ", C_WARN, str(tout)), ("  skip ", C_DIM, str(skip)),
    ]
    col = 1
    for label, c, val in parts:
        _put(scr, sy, col, label, curses.color_pair(c))
        col += len(label)
        _put(scr, sy, col, val, curses.color_pair(c) | curses.A_BOLD)
        col += len(val)

    sy += 1
    if tok:
        t = f"tokens: {tok:,} output"
        if elapsed > 1:
            t += f"  ~{int(tok / elapsed)} tok/s"
        _put(scr, sy, 1, t, curses.color_pair(C_ACCENT))

    sy += 1
    gw = min(40, w - 4)
    if ml:
        _gauge(scr, sy, 1, gw, min(1.0, loop / ml), f"loops {loop}/{ml}")
        sy += 1
    if budget:
        _gauge(scr, sy, 1, gw, min(1.0, elapsed / budget),
               f"runtime {_dur(elapsed)}/{_dur(budget)}")

    # -- Output panel --
    oy = 1 + review_h + stats_h
    _box(scr, oy, 0, output_h, w, "Agent Output")
    ih = output_h - 2
    iw = w - 4
    if ih > 0 and lines:
        if scroll:
            end = len(lines) - scroll
            start_idx = max(0, end - ih)
            visible = list(lines)[start_idx:end]
        else:
            visible = list(lines)[-ih:]
        for i, ln in enumerate(visible[:ih]):
            _put(scr, oy + 1 + i, 2, ln[:iw], curses.color_pair(C_DIM))

    # -- Footer --
    fy = h - 1
    fc = 1
    for key, desc in [("q", "quit"), ("j/k", "scroll")]:
        _put(scr, fy, fc, key, curses.color_pair(C_KEY) | curses.A_BOLD)
        fc += len(key)
        _put(scr, fy, fc, f":{desc}  ", curses.color_pair(C_DIM))
        fc += len(desc) + 3

    scr.noutrefresh()


def run_tui(runner: object, version: str = "") -> None:
    """Wrap Runner.run() in a curses dashboard.

    Runner executes in a background thread; the main thread drives curses at ~10 fps.
    """
    import builtins
    import gauntlet.cli as cli_mod
    from gauntlet.cli import ReviewResult

    st = TuiState(
        version=version,
        script_start=runner.script_start,  # type: ignore[attr-defined]
        runtime_budget=getattr(runner.args, "runtime", 0) or 0,  # type: ignore[attr-defined]
        max_loops=runner.args.max_loops,  # type: ignore[attr-defined]
        reviews=[ReviewSlot(name=r) for r in runner.reviews],  # type: ignore[attr-defined]
    )

    orig_log = cli_mod.log
    orig_print = builtins.print

    def _log(msg: str) -> None:
        line = f"[{time.strftime('%H:%M:%S')}] {msg}"
        with st.lock:
            st.log_lines.append(line)

    def _print(*args: object, **kw: object) -> None:
        text = " ".join(str(a) for a in args)
        if text.strip():
            with st.lock:
                for ln in text.splitlines():
                    st.log_lines.append(ln)

    # Patch Stats.add to update TUI counters
    real_add = runner.stats.add  # type: ignore[attr-defined]

    def _add(result: ReviewResult) -> None:
        real_add(result)
        with st.lock:
            m = {"ok": "ok", "fail": "fail", "timeout": "timeout",
                 "skipped": "skip", "interrupted": "fail"}
            attr = m.get(result.status)
            if attr:
                setattr(st, attr, getattr(st, attr) + 1)
            st.tokens += result.output_tokens or result.total_tokens or 0
            for rev in st.reviews:
                if rev.name == result.review and rev.status == "running":
                    rev.status = result.status
                    rev.elapsed = result.elapsed or 0.0
                    rev.agent = result.tool.label()
                    rev.tokens = result.output_tokens or result.total_tokens or 0
                    break

    runner.stats.add = _add  # type: ignore[attr-defined]

    # Patch run_review to mark slots as running
    real_run_review = runner.run_review  # type: ignore[attr-defined]

    def _run_review(review: str, exclude: object = frozenset()) -> None:
        with st.lock:
            for rev in st.reviews:
                if rev.name == review and rev.status == "pending":
                    rev.status = "running"
                    rev.start_time = time.monotonic()
                    break
        real_run_review(review, exclude=exclude)

    runner.run_review = _run_review  # type: ignore[attr-defined]

    done = threading.Event()
    error: list[BaseException] = []

    def _runner() -> None:
        cli_mod.log = _log
        builtins.print = _print  # type: ignore[assignment]
        try:
            runner.run()  # type: ignore[attr-defined]
        except BaseException as e:
            error.append(e)
        finally:
            cli_mod.log = orig_log
            builtins.print = orig_print  # type: ignore[assignment]
            done.set()

    def _main(scr: curses.window) -> None:
        _init_colors()
        curses.curs_set(0)
        scr.timeout(100)

        t = threading.Thread(target=_runner, daemon=True)
        t.start()

        while True:
            with st.lock:
                st.loop_count = runner.loop_count  # type: ignore[attr-defined]
                st.stopped = done.is_set()

            _render(scr, st)
            curses.doupdate()

            ch = scr.getch()
            if ch == ord("q"):
                runner.stopping = True  # type: ignore[attr-defined]
                break
            elif ch == ord("k"):
                with st.lock:
                    st.scroll = min(st.scroll + 1, max(0, len(st.log_lines) - 3))
            elif ch == ord("j"):
                with st.lock:
                    st.scroll = max(0, st.scroll - 1)

            if done.is_set():
                with st.lock:
                    st.stopped = True
                _render(scr, st)
                curses.doupdate()
                scr.timeout(-1)
                while True:
                    ch = scr.getch()
                    if ch in (ord("q"), 27):
                        break
                break

    try:
        curses.wrapper(_main)
    finally:
        cli_mod.log = orig_log
        builtins.print = orig_print  # type: ignore[assignment]

    if error:
        raise error[0]
