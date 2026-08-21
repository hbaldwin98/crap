# crap

`crap` deterministically calculates cyclomatic complexity and CRAP scores for C#, Go, TypeScript, and TSX callables. It can run as a command-line tool or an MCP stdio server.

The executable parses the source and calculates every score. An AI caller can choose paths, coverage, a Git diff, and a score threshold, but it does not derive or reinterpret the result.

## Requirements

- Go 1.23 or newer
- A C compiler available to Go, required by Tree-sitter's CGO bindings

On Windows, install a GCC or Clang toolchain and ensure its compiler is on `PATH`. On macOS, install the Xcode command-line tools. Most Linux development environments provide GCC through their package manager.

## Quick Start

Build the executable from the repository root:

```sh
go build -o crap ./cmd/crap
```

On Windows, an explicit `.exe` name is convenient:

```powershell
go build -o crap.exe ./cmd/crap
```

Analyze all supported source below the current directory:

```sh
./crap .
```

On Windows PowerShell:

```powershell
.\crap.exe .
```

You can also run it from source without building a separate executable:

```sh
go run ./cmd/crap --format json .
```

If no path is supplied, `crap` analyzes the current directory. Go `_test.go` and TypeScript `.spec`/`.test` files are excluded by default because tests produce coverage evidence but should not normally be scored as production callables.

## CLI Usage

```text
crap [options] [path ...]
crap mcp
```

Options:

| Option | Default | Purpose |
| --- | --- | --- |
| `--format text\|json` | `text` | Select human-readable or versioned JSON output. |
| `--coverage PATH` | none | Read Cobertura XML or a native Go coverprofile. |
| `--diff-base REVISION` | none | Return only callables touching lines changed from a Git revision. |
| `--threshold SCORE` | `30` | Mark scores strictly greater than this value as above threshold. |
| `--fail-on-threshold` | `false` | Exit with code `2` when any returned callable is above threshold. |
| `--include-tests` | `false` | Include Go `_test.go` and TypeScript `.spec`/`.test` files. |
| `--version` | | Print the version. |

Put options before paths. Paths are resolved from the current working directory and can be individual files or directories.

Analyze selected paths with a maximum allowed score of 20:

```sh
./crap --threshold 20 --fail-on-threshold ./internal ./cmd
```

The threshold does not change or cap calculated scores. It sets `aboveThreshold` on each result and controls `--fail-on-threshold`. A score equal to the threshold passes; a score greater than it fails.

Exit codes:

| Code | Meaning |
| --- | --- |
| `0` | Analysis succeeded and no requested threshold failure occurred. |
| `1` | Arguments, source parsing, coverage, Git, or output failed. |
| `2` | Analysis succeeded, but at least one score exceeded the threshold while `--fail-on-threshold` was set. |

## Coverage

Coverage is optional. Without a coverage report, each callable has `coveragePercent: null` and is conservatively scored as if it had 0% coverage.

For a Go project, generate a coverprofile and analyze it from that project's root:

```sh
go test -coverprofile=coverage.out ./...
crap --coverage coverage.out .
```

To include coverage for packages that have no direct tests, generate the profile with `-coverpkg`:

```sh
go test -coverpkg=./... -coverprofile=coverage.out ./...
crap --coverage coverage.out .
```

For C# or TypeScript, export coverage in Cobertura XML format with the test runner or coverage tool used by the project, then pass that file:

```sh
crap --coverage coverage.xml src/Example.cs
```

The same command accepts TypeScript coverage from tools that emit Cobertura:

```sh
crap --coverage coverage/cobertura-coverage.xml src
```

For Angular v22, the CLI can run the specs and emit Cobertura without another coverage tool:

```sh
ng test --coverage --coverage-reporters=cobertura
crap --coverage coverage/cobertura-coverage.xml --threshold 20 --fail-on-threshold src
```

Angular writes reports below `coverage/`; multi-project workspaces may add the project name to the path. Pass the generated `cobertura-coverage.xml` path to `crap`. Older Angular projects using Karma may use `--code-coverage` and need `cobertura` enabled in their Karma coverage reporter configuration.

The `.spec.ts` and `.test.ts` files execute and generate coverage for application code, but `crap` excludes those test files from scoring unless `--include-tests` is set. Angular `.html` templates are not analyzed; CRAP scores cover the TypeScript component, service, directive, pipe, and other callable logic.

Accepted formats:

- Cobertura XML, matched to C#, Go, TypeScript, or TSX source by normalized file path
- Native Go coverprofiles produced by `go test -coverprofile`

Cobertura coverage is the percentage of instrumented lines in a callable that have hits. Go coverprofile coverage is weighted by each block's statement count. A callable missing from a supplied report is scored with 0% coverage and retains `coveragePercent: null` so missing data is not mistaken for measured zero coverage.

## Changed Code

Use `--diff-base` to report only callables that intersect added or modified lines relative to a Git revision:

```sh
./crap --diff-base main --format json .
```

The command must run in a Git worktree when this option is used. Untracked `.cs`, `.go`, `.ts`, and `.tsx` files count as entirely changed. Deleted code has no callable in the current source tree, so it is not scored.

A typical changed-code CI check is:

```sh
./crap --coverage coverage.out --diff-base origin/main --threshold 20 --fail-on-threshold .
```

## MCP Server

Start the stdio server with:

```sh
./crap mcp
```

Example MCP client configuration:

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

Use an absolute executable path because an MCP client may start the server from a different working directory. On Windows, use a path such as `C:\\tools\\crap.exe` in JSON.

The server exposes one tool, `analyze_code`. Its inputs are:

| Input | Type | Default | Purpose |
| --- | --- | --- | --- |
| `root` | string | server working directory | Resolve source paths, coverage, and Git revisions from this directory. |
| `paths` | string array | `root` | Analyze these files or directories, relative to `root`. |
| `coveragePath` | string | none | Read Cobertura XML or a Go coverprofile, relative to `root`. |
| `diffBase` | string | none | Return only callables changed from this Git revision. |
| `crapThreshold` | number | `30` | Mark scores strictly greater than this value as above threshold. |
| `includeTests` | boolean | `false` | Include Go `_test.go` and TypeScript `.spec`/`.test` files. |

Example tool input with a maximum allowed score of 10:

```json
{
  "root": "C:\\source\\my-project",
  "paths": ["."],
  "coveragePath": "coverage.out",
  "diffBase": "origin/main",
  "crapThreshold": 10
}
```

The structured response uses the same report as `crap --format json`. Check `summary.aboveThreshold` for the number of violations, `summary.maximumCrap` for the highest actual score, and each method's `aboveThreshold` field for individual violations. The MCP server returns findings rather than a process exit code.

`schemaVersion` identifies the JSON contract and changes when an incompatible report change is made.

## Score Definition

The score uses the original CRAP formula:

```text
CRAP = complexity^2 * (1 - coverage)^3 + complexity
```

`coverage` is a fraction from `0` to `1`. Scores and percentages are rounded to two decimal places. CRAP is not a percentage: it combines cyclomatic complexity with test coverage. Higher complexity and lower coverage produce a higher score.

For example, a callable with complexity 4 and 0% coverage scores 20:

```text
4^2 * (1 - 0)^3 + 4 = 20
```

Complexity starts at `1` for each callable.

C# adds one for each `if`, loop, `catch`, non-default switch label, switch expression arm, ternary, `and`/`or` pattern, and `&&`, `||`, or `??` expression. Nested C# local functions are scored separately and do not contribute branches to their containing method.

The bundled C# grammar supports C# 1 through C# 13. The analyzer rejects syntax errors rather than silently producing a partial score.

Go adds one for each `if`, `for`, non-default `case`, and `&&` or `||` expression.

TypeScript and TSX add one for each `if`, loop, `catch`, non-default `case`, ternary, and `&&`, `||`, or `??` expression. Functions, generator functions, methods, function expressions, and arrow functions are scored separately; nested callables do not add their branches to the containing callable.

Files and callables are sorted by normalized path and source line. JSON contains no timestamps or environment-dependent IDs. Invalid syntax fails analysis instead of returning a partial score.

## Development

Run the automated checks from the repository root:

```sh
go test ./...
go vet ./...
go build ./...
```
