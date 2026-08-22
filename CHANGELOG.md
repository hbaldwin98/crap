# Changelog

This project follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Deterministic CRAP analysis for C#, Go, TypeScript, and TSX, including
  callable-aware coverage and Git changed-code filtering.
- CLI, JSON, SARIF 2.1.0, and paged MCP stdio interfaces.
- Mutation orchestration and normalized reports for Stryker.NET, Gremlins, and
  StrykerJS, with planning and setup diagnostics.
- Versioned JSON schemas and golden contract fixtures.

### Changed

- Hardened source discovery, MCP root authorization, cancellation, mutation
  engine contracts, and safe file replacement.
- Raised the minimum Go version to 1.25 and upgraded go-git and the MCP Go SDK
  to releases that address known vulnerabilities.
