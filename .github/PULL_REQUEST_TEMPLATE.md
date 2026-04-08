<!--
Thanks for sending a pull request! Please read CONTRIBUTING.md before opening.
Complete the checklist below and delete any sections that do not apply.
-->

## Summary

<!-- One or two sentences describing what this PR does and why. -->

## Type of change

- [ ] Bug fix (non-breaking)
- [ ] New feature (non-breaking)
- [ ] Breaking change (fix or feature that changes public API behavior)
- [ ] Documentation only
- [ ] Test-only change
- [ ] Refactor / internal cleanup
- [ ] Build / CI / tooling

## Related issues

<!-- e.g. Fixes #123, Refs #456 -->

## What changed

<!-- Bullet list of the important changes in this PR. -->

-
-

## Why

<!--
Explain the motivation: the bug being fixed, the scenario being enabled,
or the trade-off being made. This is the most valuable part of a PR description.
-->

## How to verify

<!-- Steps a reviewer can take to exercise this change locally. -->

```bash
make test
# ...
```

## Checklist

- [ ] I have read **CONTRIBUTING.md**
- [ ] My commits are **signed off** (`git commit -s`) per the DCO
- [ ] I added or updated **tests** covering this change
- [ ] `make lint test` passes locally
- [ ] `make integration` passes locally (if touching supervise / ipc / agent)
- [ ] I updated **CHANGELOG.md** under `## [Unreleased]`
- [ ] I updated relevant **documentation** (README, godoc, docs/)
- [ ] For public API changes: the change has been discussed in an issue first
- [ ] No new goroutine leaks (`goleak` is clean in tests)

## Screenshots / output

<!-- Optional: paste test output, logs, or screenshots that demonstrate the change. -->
