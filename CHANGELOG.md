# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project
adheres to [Semantic Versioning](https://semver.org/).

## [v0.1.0] — 2026-10-06

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

[v0.1.0]: https://github.com/0xshikhar/go-hotpath/releases/tag/v0.1.0
