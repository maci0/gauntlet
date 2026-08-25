Fixing:
- Search and rewrite with the best tool on PATH (fall back to grep/sed only when none exist): `rg` for text, `ast-grep`/`sg` for structural search and rewrite, `patchwork` for AST-native sed, `semcode` for semantic C/C++/Rust queries (callers/callees, types) when an index exists.
- Before fixing an issue, prove it is real: trace the actual code path, read the full function (not just a fragment), and verify callers/config branches don't already handle it. Comments and names can lie; verify against the implementation. If you cannot prove it, skip it.
- Do not add defensive checks (null/bounds/validation) unless you can name a concrete path where invalid data reaches the code.
- Fix at most ~10 distinct issues this pass, highest value first, then stop. One issue may span files; a mechanical sweep of one defect across the repo counts as one issue. If a single fix would change more than 300 lines in one file, skip it.
- Make the smallest reversible diff possible. Do not delete tests. Do not change public APIs or exported symbols.
- Skip anything uncertain. Never ask questions.
- Uncommitted changes already present when you start are someone else's recent work. Do not revert, restyle, or improve them unless they are provably broken.
- A comment containing 'review-loop: keep' marks code as reviewed and intentional: never change that line or block. Never add this marker yourself.
