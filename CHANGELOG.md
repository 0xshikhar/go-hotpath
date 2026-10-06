# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/).

## [v0.1.2] — 2026-10-06

### Fixed

- **hotpathcheck never failed `go vet` on Go 1.26.** The module was pinned to
  `golang.org/x/tools` v0.30.0, whose vet protocol predates Go 1.26's: under
  `go vet -vettool`, diagnostics came back as raw JSON with exit status 0, so
  a CI gate always passed. Now on v0.50.0 (module requires Go 1.26), and the
  fixture test asserts a non-zero exit and `file:line` output.
- CI never ran: the workflow triggered on `main`, the default branch is
  `master`. It also ran tests from an uncommitted directory.
- `profile.Apply` left knobs half-applied when read-back failed; it now rolls
  back before returning the error.
- `Session.Undo` skipped restoring a previous `GOGC=0` (it reused the
  zero-means-keep sentinel). Restores now track which knobs were touched, and
  a lowered memory limit is set before GOGC so the collector is never off
  without a ceiling.
- `QuietResult.HeapLiveBefore/After` were documented as what the cycle
  reclaimed; `/gc/heap/live` only updates at cycle end, so they describe the
  live-set change between cycles. Added `HeapObjectsBefore/After` and
  `Reclaimed()` for the real number.
- `CgroupMemoryLimit` only read the cgroup root; it now resolves the
  process's own cgroup from `/proc/self/cgroup` and takes the smallest limit
  on it or any ancestor (v2, then v1). Removed a false doc claim that the Go
  runtime derives its memory limit from the cgroup.
- `bench`'s `GCPause*` fields were documented as zero before Go 1.23. The
  `/sched/pauses` histograms exist from Go 1.22 (verified with `metrics.All`
  on 1.21–1.27), so they are always reported on supported versions; the
  version notes on the newer optional metrics (Go 1.25+/1.26+) are corrected
  the same way.
- `bench`: percentile edges now come from the runtime's own histogram
  buckets instead of a reproduced table; the op count no longer overflows on
  32-bit platforms; `New` rejects rates above 1e9 ops/s.
- `cmd/hotpathcheck` pins `toolchain go1.26.8`, so `go install` on an older
  Go 1.26.x builds the binary with the patched standard library (govulncheck
  flagged three stdlib vulnerabilities reachable from go/types on go1.26.0).
- Spike E3 timed loops whose results were never read, so the compiler
  removed them (the 0.23 ns/op figure); the sums are now kept live.
- hotpathcheck: methods of generic types resolve to their annotated
  declaration; a trailing `//hotpath:allow` no longer also silences the next
  line; `//hotpath:noallocx` no longer matches `noalloc`.

### Added

- `bench.Regressed` — the percentiles that slowed by more than 10% and 1 µs,
  for failing CI jobs (`Compare` marks the same rows).
- hotpathcheck flags string concatenation and map writes; calls into `math`,
  `math/bits`, and `sync/atomic` (except `atomic.Value`) count as verified;
  diagnostics print package-relative names (`&OrderA{...}`).
- Runnable godoc examples for `profile` and `bench`.
- Spike E5 adds a non-allocating busy-neighbor control and a GC
  attribution table, separating GC interference from CPU contention.
- BENCHMARK.md re-measured on go1.22.12, go1.26.8 and go1.27.1 (3 repeats,
  medians). Corrected a false claim that the noisy-neighbor mark-assist CPU
  ran in the zero-allocation book's goroutine — that counter is
  process-wide, and a book that never allocates cannot assist.

## [v0.1.1] — 2026-10-06

### Added

- `CONTRIBUTING.md`, `CHANGELOG.md`, issue and PR templates — standard
  OSS hygiene set
- `doc/public/runtime-surface.md` — the audited `runtime/metrics` catalog

### Fixed

- `go.work` no longer references an uncommitted module (clone-safe)
- README CI badge tracks `master`; docs table links the published doc path

Note: `v0.1.0` was briefly tagged at the same content without the template
files and is superseded by this release.

## v0.1.0 — 2026-10-06 (tag withdrawn; superseded by v0.1.1)

First public release.

### Added

- **`guard`** — zero-allocation GC-interference windows (`Begin`/`End`, ~450
  ns, 0 allocs), `Result.Quiet`/`Explain`, and `Exact`/`Assert`/`AssertWithin`
  for exact `ReadMemStats`-backed test assertions
- **`profile`** — reversible runtime control: `Apply` → `Session` with
  verified read-back, `SilentWindow`, `QuietGC`, `Snapshot`, cgroup memory
  limit detection
- **`bench`** — open-loop latency harness: intended-time pacing, dual
  Latency/Service series, fixed log-linear histogram (2.0 ns/record, 0
  allocs), run-level GC CPU attribution and `/sched/latencies` +
  `/sched/pauses` delta-percentiles
- **`hotpathcheck`** — `go vet`-compatible analyzer enforcing
  `//hotpath:noalloc` (transitive, cross-package via analysis facts) with
  `//hotpath:allow` suppression
- **`internal/rtm`** — shared zero-alloc `runtime/metrics` layer with
  required/optional metric registries and `Available` probing

### Compatibility

- Library: Go 1.22+ (recommended 1.26+); CI on 1.22 / 1.26 / 1.27 ×
  linux/macOS
- `hotpathcheck`: builds on Go 1.26+, analyzes any Go version; separate
  module tag `cmd/hotpathcheck/v0.1.0`
- On Go <1.23, `bench`'s `GCPause*` fields read 0 (`/sched/pauses` does not
  exist)

[v0.1.2]: https://github.com/0xshikhar/go-hotpath/compare/v0.1.1...v0.1.2
[v0.1.1]: https://github.com/0xshikhar/go-hotpath/releases/tag/v0.1.1
