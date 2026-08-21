# crap

`crap` deterministically calculates cyclomatic complexity and CRAP scores for C# and Go code. It runs as a CLI or an MCP stdio server, emits a versioned JSON report, and can restrict analysis to callables touched by a Git diff.

The executable calculates every score. An AI caller supplies paths and options but does not interpret source code or derive the score.

## Build

Requirements: Go 1.23+ and a C compiler for Tree-sitter's CGO bindings.

```sh
go build -o crap ./cmd/crap
```

## CLI

Analyze all C# and Go files below the current directory:

```sh
./crap
./crap --format json .
```

Go `_test.go` files are excluded by default because Go coverprofiles do not instrument test function bodies. Pass `--include-tests` to analyze them explicitly.

Analyze specific paths with coverage:

```sh
./crap --coverage coverage.xml src/Example.cs
go test ./... -coverprofile=coverage.out
./crap --coverage coverage.out ./internal ./cmd
```

Analyze only functions or methods that intersect added or modified lines relative to a Git revision:

```sh
./crap --diff-base main --format json .
```

Untracked `.cs` and `.go` files count as entirely changed. Deleted code has no callable in the new source tree and is therefore not scored.

Fail a CI step when any returned callable exceeds a score:

```sh
./crap --threshold 30 --fail-on-threshold .
```

Exit code `0` means analysis completed without a configured threshold failure, `1` means the input or analysis failed, and `2` means `--fail-on-threshold` found at least one score above the threshold.

## Coverage

`--coverage` accepts either:

- Cobertura XML, matched to C# or Go source by normalized file path.
- A native Go coverprofile produced by `go test -coverprofile`.

Cobertura coverage is the percentage of instrumented lines in the callable that have hits. Go coverprofile coverage is weighted by each block's statement count. A callable absent from the supplied report has unknown coverage. Its JSON `coveragePercent` is `null`, and its CRAP score is conservatively calculated with 0% coverage.

## Score Definition

The score uses the original CRAP formula:

```text
CRAP = complexity^2 * (1 - coverage)^3 + complexity
```

`coverage` is a fraction from `0` to `1`. Scores and percentages are rounded to two decimal places. A result is `aboveThreshold` only when its score is strictly greater than the configured threshold.

Complexity starts at `1` for each callable. C# adds one for each `if`, loop, `catch`, non-default switch label, switch expression arm, ternary, `and`/`or` pattern, and `&&`, `||`, or `??` expression. Go adds one for each `if`, `for`, non-default `case`, and `&&` or `||` expression. Nested C# local functions are scored separately and do not contribute branches to their containing method.

Files and callables are sorted by normalized path and source line. JSON contains no timestamps or environment-dependent IDs. Invalid syntax fails analysis instead of returning a partial score.

## MCP

Start the stdio server:

```sh
./crap mcp
```

Example client configuration:

```json
{
  "mcpServers": {
    "crap": {
      "command": "/absolute/path/to/crap",
      "args": ["mcp"]
    }
  }
}
```

The server exposes `analyze_code` with these inputs:

- `root`: working directory for paths, Git, and coverage; defaults to the server working directory.
- `paths`: files or directories; defaults to `root`.
- `coveragePath`: optional Cobertura XML or Go coverprofile, relative to `root`.
- `diffBase`: optional Git revision; when set, only changed callables are returned.
- `crapThreshold`: optional threshold; defaults to `30`.
- `includeTests`: include Go `_test.go` files; defaults to `false`.

The tool's structured output is the same report returned by `crap --format json`. `schemaVersion` changes if that contract makes an incompatible change.
