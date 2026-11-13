## What

<!-- One or two sentences. What changes, and why now. -->

## Checks

- [ ] `make ci` passes
- [ ] `make verify` passes if the change touches tagged files or `scripts/`
- [ ] `make vuln` passes if the change touches `go.mod` or `go.sum`
- [ ] `make cover` passes if the change removes tested code
- [ ] `make artifacts VERSION=ci` and `make smoke VERSION=ci` pass if the
      change touches the release path (a cross-compiled platform, the build
      flags, or what `dist/` inventories). CI runs both on Linux and macOS
- [ ] `make repro` passes if the change touches the release path
- [ ] `CHANGELOG.md` has an entry under `## Unreleased`, if the change is
      user-visible; internal refactors need none

## Notes

<!-- Anything a reviewer cannot read off the diff: a failure you could not
     reproduce, a tradeoff, or a deferred follow-up. -->
