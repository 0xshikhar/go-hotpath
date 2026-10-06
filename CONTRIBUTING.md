# Contributing

Thanks for considering a contribution. This project deliberately stays small —
before writing code, open an issue describing the problem so we can agree it
belongs in the library rather than next to it.

## Ground rules

- **Zero allocations on measured paths.** `guard.Begin`/`End`,
  `bench`'s `Record`, and `rtm.Set.Read` are benchmarked at 0 allocs/op —
  PRs that regress this fail review, not just tests.
- **Honest claims.** Numbers in docs are measured on the machine named in
  the doc. Never write "zero allocations" from inference — measure it.
- **Degrade, don't fail.** When a runtime metric or API is version-gated,
  probe it (`rtm.Available`) and degrade — see `bench`'s `GCPause*` handling.
- **No new dependencies** for `guard`/`profile`/`bench`/`rtm`. The module
  ships with zero requires; keep it that way.

## Development setup

Go 1.22+ (1.26+ recommended). The repo is a Go workspace:

```bash
go build ./...        # root module: guard, profile, bench, internal/rtm
go test -race ./...
cd spike && go test -race ./...
```

The analyzer is a **separate module outside the workspace** (it depends on
`golang.org/x/tools` and needs Go 1.26+) — run it with `GOWORK=off`:

```bash
cd cmd/hotpathcheck && GOWORK=off go test ./...
```

## What CI checks

Matrix: Go **1.22 / 1.26 / 1.27** × ubuntu/macos. Before opening a PR run
locally:

```bash
gofmt -l .            # must print nothing (CI fails otherwise)
go vet ./...
go test -race -count=1 ./...
```

If you touch version-sensitive code, verify on the floor too:

```bash
GOTOOLCHAIN=go1.22.12 GOWORK=off go test ./...
```

## Conventions

- **Commits:** conventional style — `feat(pkg):`, `fix(pkg):`, `docs:`,
  `ci:`, `refactor(scope):` (see `git log`).
- **Analyzer changes:** any new diagnostic in `hotpathcheck` needs an
  `analysistest` fixture under `cmd/hotpathcheck/analyzer/testdata/` — every
  diagnostic requires a `// want` comment, and facts need `name:"..."`.
- **Metric names:** new `runtime/metrics` names go in `internal/rtm/names.go`
  — required metrics in `registry`, version-gated ones in `optional`, with
  the Go version noted.
- **Benchmarks:** changes to `bench` or `guard` should come with measured
  numbers (`go test -bench=. -benchmem`) in the PR description.

## Reporting issues

- **Bugs:** include `go version`, OS/arch, and the smallest reproducer.
  For measurement claims, the raw output beats a summary.
- **Feature requests:** describe the latency problem you're solving, not
  the API you want — the design discussion is where the value is.
- **Do not file:** requests for per-goroutine GC control (impossible in Go —
  the process is the boundary), or generic lint rules unrelated to the hot path.

## License

By contributing you agree your contributions are licensed under Apache-2.0.
