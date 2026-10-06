## What & why

<!-- One paragraph: the problem this solves and the approach. Link the issue. -->

## Checklist

- [ ] `gofmt`, `go vet`, `go test -race -count=1 ./...` pass locally
- [ ] Zero-alloc benchmarks still pass (`go test -bench=. -benchmem ./guard ./bench ./internal/rtm`)
- [ ] Version-gated behavior probed with `rtm.Available` / tested on `GOTOOLCHAIN=go1.22.12` where relevant
- [ ] New analyzer diagnostics have `analysistest` fixtures with `// want`
- [ ] Docs/comments updated; measured numbers cited where claimed
- [ ] No new dependencies in `guard` / `profile` / `bench` / `internal/rtm`

## Notes for reviewers

<!-- Anything surprising, alternatives considered, numbers. -->
