# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a fork of the Prometheus Node Exporter that includes an additional **gateway collector** for Algod with buffering and other features. The Node Exporter is a Prometheus exporter for hardware and OS metrics exposed by *NIX kernels, written in Go with pluggable metric collectors.

The key distinguishing feature of this fork is the `gateway.go` collector in the `collector/` directory, which provides enhanced functionality for Algorand's needs.

## Development Commands

### Building
```bash
make                    # Build with all checks (style, vet, staticcheck, checkmetrics, build, package, test)
make build              # Build binaries only
make package            # Create tarball package
```

### Testing
```bash
make test               # Run all tests
make test-short         # Run short tests only
make test-e2e           # Run end-to-end tests (Linux only)
make test-32bit         # Cross-architecture testing (when supported)
```

### Code Quality
```bash
make style              # Check code formatting
make vet                # Run go vet
make staticcheck        # Run staticcheck linter
make checkmetrics       # Validate metrics with promtool
make format             # Format code
```

### Docker
```bash
make docker             # Build Docker image
make test-docker        # Test Docker image
```

## Code Architecture

### Core Components

1. **Main Entry Point** (`node_exporter.go`): HTTP server that exposes metrics at `/metrics` endpoint
2. **Collector Framework** (`collector/collector.go`): Plugin architecture for metric collectors
3. **Gateway Collector** (`collector/gateway.go`): Algorand-specific collector with buffering and POST handling
4. **Individual Collectors** (`collector/*_*.go`): Platform-specific metric collectors

### Collector System

The Node Exporter uses a plugin-based collector system:
- Collectors are registered via `registerCollector()` with default enabled/disabled state
- Each collector implements the `Collector` interface with `Update(ch chan<- prometheus.Metric) error`
- Collectors can be enabled/disabled via `--collector.<name>` and `--no-collector.<name>` flags
- The main collector orchestrates concurrent collection from all enabled collectors

### Gateway Collector Architecture

The gateway collector (`collector/gateway.go`) provides unique functionality:
- Acts as HTTP middleware, intercepting POST requests to `/metrics`
- Parses Prometheus format metrics from POST body
- Maintains internal counters and gauges with automatic expiration
- Implements buffering with configurable sample intervals
- Provides metric lifecycle management (creation, updating, expiration)

## Platform Support

Collectors have varying support across operating systems:
- **Linux**: Full support for most collectors
- **Darwin/macOS**: Limited support for core metrics
- **BSD variants**: Platform-specific implementations
- **Windows**: Use WMI exporter instead

## Build System

- Uses Go modules (`go.mod`) with Go 1.23+
- Makefile-based build system with `Makefile.common` for shared rules
- Cross-compilation support via `GOOS`/`GOARCH` environment variables
- CGO usage controlled by `.promu-cgo.yml` vs `.promu-no-cgo.yml`
- CI/CD via GitHub Actions (`.github/workflows/build.yml`)

## Testing Infrastructure

- Extensive fixture-based testing in `collector/fixtures/`
- Mock `/proc` and `/sys` filesystem data for reproducible tests
- End-to-end testing with expected output validation
- Platform-specific test execution based on build environment

## Key Design Principles

1. **Machine Metrics Only**: Focus on hardware/OS metrics, not application metrics
2. **No External Commands**: Use only system calls, proc/sys files, and local sockets
3. **No Root Privileges**: All metrics must be accessible without elevated permissions
4. **Hardware Agnostic**: Expose raw values without vendor-specific transformations
5. **Performance First**: Concurrent collection with minimal overhead