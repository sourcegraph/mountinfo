# AGENTS.md

## Overview

`github.com/sourcegraph/mountinfo` is a Go library providing a Prometheus
collector that advertises the names of the block storage devices backing
requested file paths. The public API entry point is `NewCollector` in
[info.go](./info.go).

## Layout

- `info.go` — public API (`NewCollector`) and the `mount_point_info` collector.
- `device.go` — shared device-resolution interface.
- `device_linux.go`, `device_darwin.go`, `device_unix.go`, `device_windows.go` —
  per-platform implementations (build-tag constrained).
- `*_test.go` — unit tests, including Linux-specific tests.
- `testdata/` — fixtures used by tests.

## Setup

- Go 1.25 (see [go.mod](./go.mod)).
- Fetch dependencies: `go mod download`.

## Build, test, lint

```bash
# Build
go build ./...

# Test (matches CI)
go test -v -race ./...

# Lint (golangci-lint v2; config in .golangci.yml)
golangci-lint run
```

## Conventions

- Format Go with `goimports` (configured as a formatter in `.golangci.yml`).
- Go files use tab indentation; other files use spaces per [.editorconfig](./.editorconfig).
- Enabled linters: `bodyclose`, `forbidigo`, `gocritic`, `govet`, `ineffassign`,
  `staticcheck`, `unconvert`, `unparam`, `unused` (see `.golangci.yml`).
- Platform-specific code lives in `device_<platform>.go` files; keep changes
  guarded by the appropriate build constraints.
- Shell scripts are formatted with `shfmt -i 2 -ci -bn` (enforced in CI).

## CI

CI (`.github/workflows/pipeline.yml`) runs on pushes to `main` and pull requests:
`go test -v -race ./...`, `golangci-lint`, and `shfmt`.
