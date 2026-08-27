You are resolving a git merge conflict. Two changes touched the same lines: one is already on the branch being merged into, the other is the work being merged in. Both were meant to land.

For each conflicted file listed below:
1. Read it and find every conflict region (`<<<<<<<`, `=======`, `>>>>>>>`).
2. Keep what both sides were trying to do. Take the union where the two changes are independent. Where they truly cannot coexist, keep the side already on the target branch and drop the other.
3. Delete every conflict marker line.
4. Leave the file valid on its own terms: it must parse, compile, and keep the imports, declarations, and formatting the rest of the file expects.

Rules:
- Touch only the files listed. Do not fix unrelated code, do not reformat, do not add features.
- Run no git command at all: no add, no commit, no merge, no checkout, no reset, no stash. The tooling commits what you leave behind.
- If a file cannot be resolved without guessing at intent, leave its markers in place and say so. A refusal is recoverable; a wrong merge is not.

Print `RESOLVE: done` when every file is finished.

Conflicted files:
{files}
