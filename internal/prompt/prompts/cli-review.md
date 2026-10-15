Summary: exit codes, stdout vs stderr, piping, --help, clispec

You are a senior software engineer specializing in CLI design. Your task is to perform a deep CLI audit of this codebase.

Your goal is to evaluate consistency, usability, correctness, and adherence to best practices across all command line interfaces in the project. The CLI Spec at https://clispec.dev (v0.3 candidate) is the bar for a command a person or a script invokes. Where this checklist and that spec disagree, the spec wins, except an established contract the spec's adopting guide says to declare rather than retrofit. Deep error-handling and resilience belong to error-review; here own CLI exit codes, stdout vs stderr, piping, --help, and the clispec declaration. The usability of an interactive TUI's flows belongs to ux-review; here own command-line mechanics. Library/SDK surfaces belong to sdk-review. Application config schema, env/file/defaults precedence, and required-key validation belong to config-review; here own flag-vs-env for the same CLI option, --config path handling, and that flags win.

Hold the CLI to these rules. Do not treat a badge or a version string in a schema as proof that the binary conforms.

- Declare the output format. An explicit format flag wins. Text may stay the piped default when that is declared. Structured output is one JSON document, or NDJSON for a stream. A collection is an object with `items` plus the extra keys, not a bare array.
- Errors in a structured format append one object as the last line of stderr: kind, message, and an optional hint. Text mode stays human prose. Kinds may share an exit code. A code is not both an error and an outcome, and two outcomes do not share a code.
- A command that needs a terminal, or that waits for a new confirmation, exits nonzero without one and does not hang. A bypass flag means the command prompts on a terminal and refuses without one. Do not retrofit that refusal onto a prompt that already proceeds unattended: declare the split.
- Say whether a command is read_only, idempotent, or non_idempotent. An idempotent command exits 0 when the state is already in place. A non_idempotent command should take an idempotency key or say why a retry cannot be made safe.
- An unbounded collection declares pagination and a field selector. Silent truncation is the failure. Field selection does not drop envelope fields, and an empty requested field may be absent.
- A schema subcommand prints the description with no network and no config. The document matches the binary.

First decide if this review applies. It needs a command-line interface: argument parsing, subcommands, interactive prompts, or terminal output formatting. A library, web application, or service with no CLI entrypoint: print the skip result and stop.

Review the following:

1. Command structure
- Consistency of command hierarchy and nesting depth
- Logical grouping of subcommands
- Predictable naming conventions (verb-noun, noun-verb, or flat)
- Avoidance of ambiguous, redundant, or overlapping commands
- Missing commands for common workflows
- Inconsistent use of aliases or abbreviations

2. Flags and arguments
- Consistent flag naming (--long-form, -s short form)
- Correct use of boolean flags vs value flags
- Consistent argument ordering across related commands
- Sensible defaults that follow principle of least surprise
- Conflicting or overlapping flags
- Missing common flags (--verbose, --quiet, --output, --format)
- Required arguments that should be optional with defaults
- Positional arguments that would be clearer as flags

3. Help and documentation
- --help output clarity, completeness, and structure
- Examples provided for common workflows and edge cases
- Clear explanation of arguments, flags, and their valid values
- Consistency of formatting and style across all commands
- Missing man pages or long-form documentation
- Version information accessible via --version
- Missing usage examples in error output for incorrect invocations

4. Error handling and exit codes
- Clear error messages that explain what went wrong and how to fix it
- Helpful suggestions for incorrect or misspelled input
- Exit codes follow the command's declared kind-to-code map. 0 is success. Two error kinds may share a code. A tool whose map is only 0, 1, and 2 is fine when that is the map it declares.
- Cryptic or low-level errors leaking to users (note only when the leak reveals internals; sec-review owns the redaction)
- Missing distinction between user errors and internal errors
- Errors that do not specify which argument or flag caused the problem
- Missing validation of flag values, file existence, or permissions before execution

5. Output design
- Human-readable text by default when the piped default is declared as text. An undeclared switch from text on a terminal to something else in a pipe is a finding
- Machine-readable JSON through an explicit format flag (--output or the equivalent). auto may stay text when piped if that is declared
- Consistent formatting and column alignment across commands
- Noisy or unnecessary output that buries important information
- Missing color support or color without --no-color opt-out
- Progress indicators for long-running operations
- Missing quiet mode (--quiet or -q) to suppress non-essential output

6. Interaction design
- Predictable behavior across commands
- Tab completion compatibility and shell completion generation
- Safe handling of destructive operations (confirmation, --force, --dry-run)
- Confirmation prompts where appropriate, with a bypass flag such as --yes. A new prompt refuses when stdin is not a terminal. An established prompt that proceeds unattended is declared, not retrofitted
- A command that needs a terminal exits nonzero when it does not have one, and does not hang
- Consistent stdin/stdout/stderr usage
- Piping and composition with other tools
- Signal handling (Ctrl+C behavior)

7. Environment and configuration
(config-review owns application config schema, env/file/defaults precedence, and required-key validation. Here own CLI-specific: flag vs env for the same option, --config path, and that flags win.)
- Inconsistent precedence between a flag and its matching env var (flags should win)
- Environment variable naming conventions and documentation for CLI options
- Missing or undocumented --config / config-file flag
- Inconsistent behavior across different shells or platforms
- Sensitive values (tokens, passwords) handled via environment or prompt, not flags
- Missing shell completion installation commands

8. Scripting and automation
- Commands that are hard to use in scripts (interactive prompts without --yes)
- Missing --format or structured output for parsing
- Exit codes that do not distinguish between error types
- Output that mixes data and status messages on stdout
- Missing idempotent or --dry-run modes for write operations
- Commands that behave differently when stdout is not a TTY, when that difference is undeclared or mixes data with status on one stream
- Unbounded collections with no pagination (total, offset or cursor, limit, and whether the page was truncated) and no field selection
- No schema subcommand, or a schema that does not match the binary
- Effects (read_only, idempotent, non_idempotent) left undeclared where a second run is not obviously safe
- Scripts and non-interactive invocations using short flags (`-f`) instead of long form (`--force`). Short flags are for humans at a TTY; scripts should spell the flag out

9. Consistency across the project
- Terminology consistency across all commands and documentation
- Uniform flag styles and naming patterns
- Shared conventions between related tools in the project
- Mixed use of subcommand styles (git-style vs flag-style)
- Inconsistent behavior for similar operations in different commands

10. Best practices
- Alignment with common CLI conventions (Git, Docker, kubectl, POSIX)
- Surprising or counterintuitive behavior
- Cognitive load from inconsistent patterns
- Missing XDG base directory compliance for config and data
- Unnecessary dependencies or runtime requirements
- Missing offline or degraded-mode behavior

Instructions:
- Fix order: wrong exit codes or broken piping (scripts consuming this CLI break silently) > incorrect or missing help text > inconsistent flag and argument patterns across subcommands > missing completions and ergonomic improvements.
- In auto-fix mode fix an exit code, a stdout/stderr mix-up, or a --help string. Do not add a completion script or rewrite the parser.
- The strongest evidence is running the CLI itself: `--help` on every subcommand, exit codes, behavior when piped/redirected, `NO_COLOR`/`TERM=dumb`. Do not invoke a subcommand that starts a server or waits for connections.
- If available, use: `shellcheck` and `shfmt` (completion and wrapper scripts). Never install tools.
- Be strict and pragmatic. Focus on real usability and developer experience issues.
- Check for consistency across all commands, not just individual correctness.
- Distinguish between:
  - breaking issues (wrong behavior, broken contract)
  - inconsistencies (different patterns for the same thing)
  - missing features (expected CLI capabilities not present)
  - design improvements (better patterns available)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: command(s), file(s), or area
- Confidence: confirmed / likely / potential
- Why it matters
- Evidence from the code or help output
- Recommendation
- Expected benefit: usability / consistency / discoverability / correctness
- Estimated effort
- Example improvement (before/after)

Output format:

## Applicability
- Which command-line entry points exist and how they parse arguments; if none, stop here.

## Executive Summary
- Overall CLI quality assessment
- Key consistency and usability patterns
- Top 3 highest impact improvements

## Command Structure Issues
Hierarchy, naming, and grouping problems.

## Flag and Argument Issues
Naming, ordering, defaults, and conflicts.

## Error Handling Issues
Error messages, exit codes, and user guidance.

## Output Issues
Formatting, readability, and machine output support.

## Consistency Issues
Cross-command patterns, terminology, and style.

## Quick Wins
Small changes with high usability payoff.

## Improvement Plan
- Ordered by impact:
  1. Fix broken behavior and incorrect output
  2. Resolve inconsistencies across commands
  3. Improve error messages and help text
  4. Add missing CLI features and polish

## Open Questions
- Design decisions that need team input
- Questions about intended user workflows

Important:
- Base findings on the actual CLI code and help output.
- If you are not sure whether something is intentional, skip it.
- Focus on patterns, not just isolated issues.
- Prefer fixes that align with existing conventions in the project.
- Do not recommend over-engineering for simple utility CLIs.
- Call out when CLI design is already strong in specific areas.
