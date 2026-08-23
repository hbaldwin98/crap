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
- Deterministic actual-change scope reports through CLI and immutable MCP tools,
  with Git ranges, changed callable seeds, containment evidence, and explicit
  limitations.
- Direct-Git baseline comparison through CLI and immutable MCP tools, including
  conservative callable move matching, explicit ambiguity, quality deltas, and
  a CI gate that fails only for new threshold regressions.
- A deterministic language-neutral code graph with logical modules, bounded
  selected-source dependency resolution, explicit unresolved import evidence,
  file/type/callable inventory, immutable paged MCP access, and coherent bounded
  neighborhoods. `crap graph --format json` exposes the full graph as an
  AI-consumable structural contract; `text` summarizes it.
- Architecture analysis through `crap arch`: declarative JSON dependency rules
  with anchored glob matching, deterministic strongly-connected-component cycle
  witnesses backed by import references, an exit code usable as a CI gate, and
  published v1 JSON schemas for reports and rules inputs.
- Compiler-backed call graphs through `crap calls` for Go modules: static and
  bounded-dispatch call edges resolved from compiler type information with
  capped call-site evidence, explicit unresolved calls, and reverse traversal
  from changed callables to affected tests via `--diff-base`, with a published
  v1 JSON schema.

### Changed

- Bounded Go mutation concurrency to one worker and one test CPU by default,
  with explicit capped CLI and MCP overrides.
- Hardened source discovery, MCP root authorization, cancellation, mutation
  engine contracts, and safe file replacement.
- Raised the minimum Go version to 1.25 and upgraded go-git and the MCP Go SDK
  to releases that address known vulnerabilities.
