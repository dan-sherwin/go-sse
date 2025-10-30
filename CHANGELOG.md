# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog, and this project adheres to Semantic Versioning.

## [Unreleased]

### Security
- Bump minimum Go version to 1.25.3 and require toolchain `go1.25.3` to address standard library vulnerabilities (GO-2025-4007, GO-2025-4008, GO-2025-4010, GO-2025-4011, GO-2025-4013).

## [v0.1.3] - 2025-10-30

### Added
- Initial public release of the Go SSE helper for Gin with:
  - Broadcast and targeted events
  - Event replay via `Last-Event-ID`
  - Automatic heartbeats and retry hints
  - Proper SSE and CORS headers
  - Graceful shutdown by session ID(s)
- README badges (Go Reference, Go Report Card, CI, License, Version)
- GitHub Actions CI workflow (`.github/workflows/ci.yml`)
- Linting configuration (`.golangci.yml`)
- MIT `LICENSE`

### Changed
- N/A

### Fixed
- N/A

---

[Unreleased]: https://github.com/dan-sherwin/go-sse/compare/v0.1.3...HEAD
[v0.1.3]: https://github.com/dan-sherwin/go-sse/releases/tag/v0.1.3
