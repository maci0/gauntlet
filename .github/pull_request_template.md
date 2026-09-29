## What

<!-- One or two sentences. What changes, and why now. -->

## Checks

- [ ] `make ci` passes (run it before pushing, not after the red check)
- [ ] `make verify` passes if the change touches tagged files or `scripts/`
- [ ] `make vuln` passes if the change touches `go.mod` or `go.sum` (CI
      runs govulncheck on exactly that pull request)
- [ ] `make cover` passes if the change removes tested code (CI gates on a
      coverage floor)
- [ ] `make dist` and `make repro` pass if the change touches the release
      path (a cross-compiled platform or the build flags)
- [ ] `CHANGELOG.md` has an entry under `## Unreleased`, if the change is
      user-visible; internal refactors need none

## Notes

<!-- Anything a reviewer cannot read off the diff: a failure you could not
     reproduce, a decision you went back and forth on, a follow-up you are
     deliberately leaving out. -->
