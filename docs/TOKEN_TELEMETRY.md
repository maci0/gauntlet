# Reading live token throughput from agent CLIs

How gauntlet knows how fast an agent is generating, what it does when the agent
will not say, and what it would take to get the number out of the network layer
instead.

## The problem

The dashboard wants a live tok/s per agent. Nothing about that is guaranteed:

- Some CLIs print usage as they stream, some print it once at exit, some never
  print it at all.
- The shapes differ (`"output_tokens": 1234`, `tokens used: 12,345`,
  `Output tokens: 1234`), and they change between releases.
- A number that is wrong is worse than a number that is missing, because a
  dashboard is read at a glance and nobody re-derives it.

So the rule throughout: **measure or say nothing**. There is no interpolation,
no estimate from character counts, no "probably about" anywhere in this path.

## What is implemented

Three independent sources feed the same `usage` event. Whichever reports more
wins, since all are cumulative for the review.

### Source 1: the agent's own stdout, parsed as it streams

`internal/agent/usage.go` holds the counter patterns; `internal/runner/exec.go`
runs them per line behind the cheap substring gate `MayCarryUsage`, so the
regexes never see the thousands of lines that cannot match. When the number
grows, the runner publishes a `usage` event.

Costs nothing extra (the lines are already being scanned for the feed), works
for any agent that prints a counter, and needs no configuration.

### Source 2: the agent's own session transcript, tailed

`agentusage` in [toktop](https://github.com/maci0/toktop) reads the JSONL
transcripts the CLIs write for their own session history. Gauntlet links it
unless a build opts out (see "Where this code lives" below). The formats
below were verified against live transcripts on a real machine:

**claude** writes `~/.claude/projects/<slug>/<session-uuid>.jsonl`, one JSON
object per line. The interesting records are:

```json
{"type":"assistant","cwd":"/home/dev/project","timestamp":"2026-08-25T00:00:00Z",
 "message":{"role":"assistant","usage":{
   "input_tokens":2,"cache_creation_input_tokens":44174,
   "cache_read_input_tokens":0,"output_tokens":263}}}
```

Usage is **per message**, so the values are summed. Every record repeats `cwd`,
which is what attributes a transcript to a review.

**codex** writes `~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl`:

```json
{"type":"session_meta","payload":{"id":"…","cwd":"/home/dev/project", …}}
{"type":"event_msg","payload":{"type":"token_count","info":{
   "total_token_usage":{"input_tokens":29127,"output_tokens":176,
                        "total_tokens":29303}, …}}}
```

Usage is **cumulative for the session**, so the watcher records the first value
it sees as a baseline and reports the difference. Only `session_meta` carries
`cwd`, so ownership is decided once from the head of the file and cached.

**qwen** writes `~/.qwen/projects/<slug>/chats/<session>.jsonl` with
Gemini-style metadata on each assistant message:

```json
{"type":"assistant","cwd":"/home/dev/project","usageMetadata":{
  "promptTokenCount":38918,"candidatesTokenCount":205,
  "thoughtsTokenCount":82,"totalTokenCount":39123}}
```

Per message again, and thinking tokens are output tokens, so both are counted.

**gemini** keeps transcripts under `~/.gemini/tmp/<workspace>/chats/` but the
records checked carried no usage metadata, so it has no adapter. If a newer
release adds one, it is a `parse` function and a table entry away.

The watcher handles the three things that make this correct rather than
approximately right:

1. **Only this review's tokens.** Files that already exist when a review starts
   are read from their current end, so a session reused by
   `--continue-sessions` contributes only what it adds from now on.
2. **Only this review's session.** A transcript whose recorded working
   directory is not the review's is skipped. In `--jobs` mode every review has
   its own worktree path, so this separates concurrent reviews exactly.
3. **Cheaply.** The store is walked at most once a second (these directories
   hold thousands of old sessions), and a file whose mtime has not moved is not
   reopened.

An agent with no adapter (`gemini`, `grok`, `agy`, …) simply has
no watcher, and its lane shows no rate. Adding one is a `parse` function plus a
table entry, and the tests carry real record shapes as fixtures so a format
change fails loudly instead of silently returning zero.

### What the dashboard does with it

Successive cumulative values become a rate per lane, smoothed with a light
exponential average so the number is readable without hiding a real change. A
lane whose agent reports nothing shows no rate at all. The always-available
signal, shown for every agent, is the output line rate in the activity chart.

### Source 3: the agent's machine-readable output mode (`--stream`)

Most CLIs can emit JSONL instead of prose. Every flag below was read from that
CLI's own `--help` on a machine where it is installed, because an unknown flag
makes an agent exit instead of run:

| Agent | Flag |
|---|---|
| claude | `--output-format stream-json --verbose` |
| gemini | `--output-format stream-json` |
| qwen | `--output-format stream-json` |
| kimi | `--output-format stream-json` |
| cursor-agent | `--output-format stream-json` |
| grok | `--output-format streaming-messages-json` |
| clanker | `--stream` |

`--stream` turns this on for whichever agents in the pool support it; the rest
are launched exactly as before. The stream gives three things text mode hides:
usage as it accrues, the reasoning/output split, and text with no spinners or
escape codes in it.

`internal/streamjson` deliberately models **no** agent's envelope. It walks the
decoded JSON and collects values by key, with one rule that makes it survive
dialect differences: a text-ish key contributes text only when its value is a
string, and any container is descended into instead. So `message` can be a
string in one agent and an object holding counters in another, and both work.
A shape nobody anticipated contributes nothing rather than something wrong, and
a line that is not JSON at all (a warning, a stack trace) falls through to the
ordinary text normalizer so it is never swallowed.

Reasoning is separated from output wherever the agent marks it: a block typed
`thinking`/`reasoning`, or a field named `thinking`, `reasoning`, `thought`, or
`reasoning_content`. Once inside such a block, nested plain text is reasoning
too, which is what grok's `reasoning_delta` events need.

## Coverage today

Every entry below was checked on a machine where the agent is installed: flags
from its own `--help`, transcript layouts from its own session files.

| Agent | Live tokens | Reasoning | Source |
|---|---|---|---|
| claude | yes | yes | transcript (`~/.claude/projects`), and `--stream` |
| codex | yes | yes | transcript (`~/.codex/sessions`, `token_count` events) |
| qwen | yes | yes | transcript (`~/.qwen/projects`), and `--stream` |
| pi | yes | yes | transcript (`~/.pi/agent/sessions`), and `--mode json` |
| prime-agent | yes | yes | transcript (`~/.prime/agent/sessions`), and `--mode json` |
| feynman | yes | yes | transcript (`~/.feynman/sessions`); no json mode |
| gemini | with `--stream` | with `--stream` | stream only: its transcripts carry no usage |
| kimi | with `--stream` | if reported | stream only: sessions exist under `~/.kimi-code/sessions` but are keyed by a hashed directory name, so they cannot be attributed to a review |
| cursor-agent | with `--stream` | if reported | stream only: sessions are opaque SQLite blobs |
| grok | with `--stream` | yes (`reasoning_delta`) | stream only: its transcript records a context total, not output |
| omp | with `--stream` | if reported | definition unverified (the installed copy would not run) |
| clanker | yes | no | its own `state/token_stats.jsonl`, inside the repository it runs in, and `--stream` |
| dsh | yes | yes | transcript (`~/.dsh/sessions`, default `session.jsonl.zstd`; uncompressed `.jsonl` too) |
| crush | yes | no | records per-session `prompt_tokens`/`completion_tokens` in `.crush/crush.db` (SQLite) at the project root it resolves; the only JSONL it writes is `.crush/logs/crush.log`, which carries no counters. Read by toktop's `agentusage` with `-tags sqlite`, no flag needed: the database is inside the tree being reviewed, not an operator-wide store like opencode's |
| opencode | with `--opencode-db` | with `--opencode-db` | sessions in `~/.local/share/opencode/opencode.db` (XDG_DATA_HOME honored), one row per message with usage in a JSON column; the store holds every project on the machine, so reading it is opt-in |
| agy | only if it prints a counter | no | no machine-readable mode and no transcript store found |

Every agent, including the last row, still contributes the always-available
signal: the output line rate in the activity chart, plus any counter it prints,
which is parsed from the stream as it arrives.

### The pi family

`pi` is a framework as much as a CLI, and several agents are built on it
(`prime-agent`, `feynman`, `omp`). They are not compiled into gauntlet: they
ship as **definitions**, one JSON object each, giving the argv, the flags, and
where the agent keeps its transcripts. `gauntlet doctor` lists them alongside
the built-ins and says which are unverified.

A transcript root may be inside the reviewed project rather than under `$HOME`:
`{dir}` in a root expands to the review's working directory. That is how
clanker is read, since it logs every request to `state/token_stats.jsonl` in
the repository it runs in.

Anyone can add another, without a new binary. In `~/.gauntlet/agents.json`
(plain JSON: comments and trailing commas are refused at startup):

```json
{
  "myagent": {
    "argv": ["myagent", "-p", "{prompt}"],
    "model": ["--model", "{model}"],
    "effort": ["--effort", "{effort}"],
    "stream": ["--mode", "json"],
    "continue": ["-c"],
    "usage": {"roots": ["~/.myagent/sessions"]}
  }
}
```

or for one run: `--agent-cmd myagent='myagent -p {prompt}'`.

Transcripts of defined agents are parsed by the same key-walking used for the
stream, so any JSONL carrying recognizable counters and a `cwd` works. That is
how `pi`, `prime-agent`, and `feynman` get live tokens with no agent-specific
code: their records differ (`usage.output`, `usage.totalTokens`,
`usage.reasoning`) and all three are read correctly.

### dsh: the default session log is zstd

dsh (`@deepseek-ai/dsh`) is a plugin harness. Reading its packages shows the
shape exactly:

- `dsh-session-persistence-jsonl` writes
  `<root>/--<normalized-cwd>--/<session-id>/session.jsonl.zstd`, where the
  first record is a header carrying `cwd`. Uncompressed `.jsonl` is the
  same records when compression is off.
- `dsh-base` configures that plugin with `root: dshHomePath('sessions')`, so
  the root is `~/.dsh/sessions`.
- Completed `assistant/message` records carry the provider's
  `inputTokens`, `outputTokens`, and `reasoningTokens` (the streaming
  `assistant/chunk` usage event repeats those numbers).

toktop 0.7.0 reads the concatenated zstd frames. Gauntlet does not ask dsh
to write uncompressed JSONL.

Note that dsh's own `ctx.tokenMeter` is deliberately a heuristic (four
characters per token). That number is never used here: an estimate presented
as a measurement is exactly what this design refuses.

### grok and cursor-agent: verified from the binaries

Neither ships readable source, so the event shapes were read out of the
shipped binaries with `strings`. grok emits `stream_event` records with
`text_delta`, alongside `input_tokens`, `output_tokens`, `reasoning_tokens`,
and `cached_tokens`; cursor-agent emits an Anthropic-shaped stream with a
final `result` record carrying `usage`. Both are handled by the generic
parser, and both shapes are pinned as tests.

grok also keeps `~/.grok/sessions/<url-encoded-cwd>/<id>/updates.jsonl`, but
its records carry only a cumulative `totalTokens` (the context size), not
output, so a rate computed from it would be a lie. The stream is used instead.

One operational note for pi: its non-interactive modes skip the trust prompt
and fall back to `defaultProjectTrust`, so a review that must edit files needs
`"defaultProjectTrust": "always"` in `~/.pi/agent/settings.json`. `doctor` says
so next to the agent.

## Using this from another tool

The reading side is a public package, `github.com/maci0/toktop/agentusage`,
so a monitor does not have to repeat the archaeology above:

```go
// One agent working in one directory.
w := agentusage.Watch("claude", "/home/dev/project", time.Now())
first := w.Poll()
// …later…
if rate, ok := agentusage.Rate(first, w.Poll()); ok {
    fmt.Printf("%.0f tok/s\n", rate)
}

// Or everything running on this machine.
for _, p := range agentusage.Discover() {
    fmt.Println(p.Tool, p.Dir, agentusage.Supported(p.Tool))
}
```

`Discover` walks `/proc`, so it lists processes on Linux; elsewhere it
returns nothing, which callers treat as "cannot tell". `Watch` and `Rate`
work wherever the transcripts do.

`Rate` refuses to answer without two readings and a positive span, which is the
same rule the dashboard follows: no span, no rate, rather than a zero.
`LoadDefinitions` reads the same `~/.gauntlet/agents.json`, so a tool that
imports this package inherits any agent the user has defined.

toktop is where this lives, and it uses it exactly this way: it discovers
agent processes, reads their transcripts, and shows their throughput beside the
inference engines, with no cooperation required from the agent. An agent
connected to an engine toktop is already watching is suppressed rather than
added, since counting both would double the number.

## Where this code lives

Source 1 (agent output) is gauntlet's own: `internal/streamjson` plus the
counter scan in `internal/agent`. It is always compiled in.

Source 2 (transcripts) is toktop's, and on by default.
`internal/runner/usage.go` declares the interface; `usage_toktop.go` and
`usage_off.go` supply the two implementations, and `-tags notoktop` picks the
second.

```sh
make build                    # transcripts and databases (what releases ship)
make build TAGS=notoktop      # no transcript reading, agent output alone
make build TAGS=              # and no database driver either: stdlib only
gauntlet doctor               # says which build this is
```

Two agents keep databases instead of transcripts, and the `sqlite` tag (on by
default) is what links a driver for them. crush's database is inside the
project it is working on, so the tag is all it takes. opencode's holds every
project on the machine, so it takes `--opencode-db` as well: opening an
operator's session store should be asked for. Asking for that flag in a build
without the driver is an error rather than silence.

In an opted-out build, agents that print no counter report no tokens at all,
which is the same rule as everywhere else here: measure, or say nothing.

## What was considered and not built

### A local MITM proxy

Point every agent at a local proxy through `HTTPS_PROXY`, terminate TLS with a
private CA, and read `usage` out of the provider's own SSE events. This is
provider-accurate and agent-agnostic.

Rejected because it decrypts the user's prompts and completions: a code review
tool would be sitting in the middle of its own source code leaving the machine.
It also needs a CA installed into whatever trust store each agent uses, and
some clients pin certificates.

## The eBPF route, in detail

The question that keeps coming up: can we hook the agents' HTTPS traffic and
read token counts out of the responses, without the agent cooperating? Yes, and
here is what it actually takes.

### Why plain packet capture does not work

The traffic is TLS. A socket-level probe (`kprobe/sys_read`, `tc`/XDP, a raw
socket) sees ciphertext. To read the SSE body you must be inside the process,
on the boundary between the application and its TLS library. That means
**uprobes on the TLS library's read and write functions**, which is exactly how
Pixie and the OpenTelemetry eBPF instrumentation do TLS visibility.

### Where to attach

| Runtime | Symbol to probe | Notes |
|---|---|---|
| Python (`pip`-installed CLIs) | `SSL_read`, `SSL_write` in `libssl.so` | Easiest case: dynamically linked, symbols exported, one uprobe per function. |
| Node (`claude`, `gemini`, `qwen`, `kimi`, …) | BoringSSL, statically linked into the `node` binary | No exported symbol. Offsets must be found per node build, usually by pattern-scanning `.text`, and they move between versions. |
| Bun (`opencode`, `dsh` via `bunx`) | BoringSSL, statically linked into `bun` | Same as node, different binary, no stable offsets. |
| Go (`codex` is Rust, but Go agents exist) | `crypto/tls.(*Conn).Write`, `.Read` | Go has no PLT for internal calls; uretprobes need the return sites, and the Go ABI puts arguments in registers, so the reader must match `GOARCH`. |
| Rust (`codex`) | `rustls` or `native-tls`; statically linked | Symbol names are mangled and inlined aggressively; often needs DWARF to locate. |

The practical consequence: **the agents gauntlet targets are mostly statically
linked JavaScript runtimes**, which is the hardest case, and the one that
breaks on every upgrade.

### What the probe would have to do

1. Attach a uprobe on the write path and a uretprobe on the read path (the
   buffer is only filled when the call returns).
2. Copy the plaintext buffer into a `BPF_MAP_TYPE_PERF_EVENT_ARRAY` or ring
   buffer, in chunks bounded by the verifier's stack limits (512 bytes per
   copy, so large bodies arrive fragmented).
3. Reassemble per connection, in userspace, keyed by `(pid, fd)`.
4. Parse **HTTP/2**: the provider APIs negotiate h2, so the stream is frames
   with HPACK-compressed headers, interleaved across streams. The SSE body sits
   inside `DATA` frames of one stream. This is a real parser, not a regex.
5. Find the `message_delta` event carrying
   `{"usage":{"output_tokens":N}}` and emit the count.

Steps 4 and 5 are where a weekend project becomes a maintained one.

### Requirements and their cost

- **Privileges**: `CAP_BPF` + `CAP_PERFMON` on modern kernels, `CAP_SYS_ADMIN`
  on older ones. Asking a code review tool to run privileged so it can draw a
  number is a poor trade, so it would have to be a separate opt-in helper.
- **Kernel**: BTF/CO-RE for portability, or per-kernel builds.
- **Platform**: Linux only. macOS is a first-class target here, and has no
  equivalent (DTrace cannot read process memory this way under SIP).
- **Data sensitivity**: those buffers contain the user's source code and the
  model's completions. Capturing them in the kernel to count tokens is
  disproportionate, and any bug in the reassembly path is a source code leak.
- **Fragility**: every node, bun, and agent release can move the offsets.

### If it were built anyway

The shape that would be acceptable:

- A separate binary, `gauntlet-probe`, never linked into the main tool.
- Runs only when explicitly enabled (`--probe`), and refuses to start without
  the capability rather than asking for it.
- Emits **counts only** over a unix socket: `{pid, stream, output_tokens}`.
  The payload never leaves the kernel-to-parser boundary, and the parser is
  the only component that ever sees plaintext.
- Fails closed: any parse error drops the connection's state and reports
  nothing, so the dashboard falls back to "no rate" rather than a wrong one.
- A conformance test per runtime, because a silent regression here is
  indistinguishable from an idle agent.

Sources 1 and 2 above produce the same number, for the agents this project
actually runs, at a fraction of that cost. That is why they exist and this
does not.
