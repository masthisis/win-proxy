<!-- Title format: <type>(<scope>): <subject> — e.g. feat(test-center): cancel button for speed tests -->

## Summary

<!-- One paragraph: what does this PR do and why? -->

## Changes

- …

## Related issues

<!-- Closes #123 / Fixes #45 / Refs #6 -->

## How was this tested?

<!-- Build command(s), Windows version, and the scenario you exercised.
     Unit tests go through `go test ./internal/...`; real-subscription and
     core-level checks are described in docs/TESTING.md. -->

- [ ] `go build` (GOOS=windows) and `go vet ./...` pass locally
- [ ] `go test ./internal/...` passes
- [ ] `npm run build` in `frontend/` passes (includes vue-tsc type check)
- [ ] Manually exercised on Windows <!-- version here -->

## Checklist

- [ ] Public behaviour changes are documented (README / docs/*)
- [ ] New service methods are documented in `docs/CONTRACT.md`
- [ ] New events are documented in `docs/CONTRACT.md`
- [ ] No secrets, subscription URLs or tokens are committed
