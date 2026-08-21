# crap

This repository contains two tools:

- `crap` deterministically calculates cyclomatic complexity and CRAP scores for C#, Go, TypeScript, and TSX callables.
- `crap-mutate` runs a language-native mutation engine and converts its output into one stable report for C#, Go, and TypeScript.

Both tools can run from the command line or as separate MCP stdio servers.

The tools parse native reports or source and calculate every score. An AI caller can choose paths, coverage, a Git diff, and score thresholds, but it does not derive or reinterpret results.

## Requirements

- Go 1.23 or newer
- A C compiler available to Go, required by Tree-sitter's CGO bindings

On Windows, install a GCC or Clang toolchain and ensure its compiler is on `PATH`. On macOS, install the Xcode command-line tools. Most Linux development environments provide GCC through their package manager.

## Quick Start

Build the executable from the repository root:

```sh
go build -o crap ./cmd/crap
go build -o crap-mutate ./cmd/crap-mutate
```

On Windows, an explicit `.exe` name is convenient:

```powershell
go build -o crap.exe ./cmd/crap
go build -o crap-mutate.exe ./cmd/crap-mutate
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
| `--strict-coverage` | `false` | Fail when a supplied coverage report has unmatched or ambiguous source paths. |
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

Cobertura coverage is the percentage of instrumented lines owned by a callable that have hits. Callable names and ranges always come from the C#, Go, or TypeScript AST; Cobertura method names are ignored because instrumentation and source-map processing can rewrite them. A nested callable owns its own lines, so its coverage is excluded from its parent. Go coverprofile coverage is weighted by each block's statement count.

Coverage paths are matched by exact normalized path, then by a unique component suffix, then by a unique case-insensitive match. Cobertura `<sources>` entries and both slash styles are supported. Non-exact matches produce deterministic diagnostics. Unmatched or ambiguous files retain `coveragePercent: null` and are conservatively scored as 0% coverage; use `--strict-coverage` in CI to reject those reports instead.

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
./crap mcp --root /absolute/path/to/project
```

Example MCP client configuration:

```json
{
  "mcpServers": {
    "crap": {
      "command": "/absolute/path/to/crap",
      "args": ["mcp", "--root", "/absolute/path/to/project"]
    }
  }
}
```

Use an absolute executable path and an explicit `--root` because an MCP client may start the server from a different working directory. On Windows, use paths such as `C:\\tools\\crap.exe` and `C:\\source\\my-project` in JSON. Repeat `--allow-root PATH` to let callers select projects under additional roots. MCP requests cannot read source or coverage files outside the selected authorized root, including through existing symlinks.

The server exposes one tool, `analyze_code`. Its inputs are:

| Input | Type | Default | Purpose |
| --- | --- | --- | --- |
| `root` | string | server `--root` | Select an existing project inside a configured `--root` or `--allow-root`. |
| `paths` | string array | `root` | Analyze these files or directories, relative to `root`. |
| `coveragePath` | string | none | Read Cobertura XML or a Go coverprofile, relative to `root`. |
| `diffBase` | string | none | Return only callables changed from this Git revision. |
| `crapThreshold` | number | `30` | Mark scores strictly greater than this value as above threshold. |
| `includeTests` | boolean | `false` | Include Go `_test.go` and TypeScript `.spec`/`.test` files. |
| `strictCoverage` | boolean | `false` | Fail when supplied coverage paths are unmatched or ambiguous. |
| `resultMode` | string | `violations` | Return `summary`, `violations`, `highest`, or `all` methods. |
| `limit` | integer | `20` | Return at most this many methods; maximum `100`. |
| `offset` | integer | `0` | Skip this many matching methods for stateless pagination. |

Example tool input with a maximum allowed score of 10:

```json
{
  "root": "C:\\source\\my-project",
  "paths": ["."],
  "coveragePath": "coverage.out",
  "diffBase": "origin/main",
  "crapThreshold": 10,
  "resultMode": "violations",
  "limit": 20
}
```

Every response includes the full analysis summary, coverage diagnostics, and a `page` object. Methods are sorted by descending CRAP score before pagination. `violations` returns only methods above the requested threshold, `highest` and `all` return all methods, and `summary` returns no methods. Use `page.nextOffset` in another call when it is not `null`; each page reruns the same deterministic analysis rather than relying on server state.

Check `summary.aboveThreshold` for the violation count, `summary.maximumCrap` for the highest actual score, and each returned method's `aboveThreshold` field. Use the CLI JSON format when one complete unpaged report is required. The MCP server returns findings rather than a process exit code.

`schemaVersion` identifies the JSON contract and changes when an incompatible report change is made.

## Mutation Testing

`crap-mutate` owns engine selection, invocation, report parsing, score normalization, sorting, and threshold evaluation. An AI caller chooses the project, language, paths, and minimum score; it does not inspect test output and invent a score.

Mutation runs are not inherently deterministic. Flaky tests, timeouts, concurrency, and machine load can change engine results. For a fixed engine report, `crap-mutate` always emits the same score, counts, ordering, and JSON.

### Install Engines

Install only the engines needed by your projects and pin them in each project where possible.

For C#, Stryker.NET currently requires the .NET 10 runtime. A local tool manifest keeps its version in source control:

```sh
dotnet new tool-manifest
dotnet tool install dotnet-stryker
dotnet tool restore
```

For TypeScript, initialize StrykerJS in the target project. This installs the core package and an appropriate test-runner plugin:

```sh
npm init stryker@latest
```

Commit the resulting package lock and Stryker configuration. `crap-mutate` invokes `npx --no-install`, so it will not download an unpinned package during a run.

For Go, install a selected Gremlins release and put the `gremlins` executable on `PATH`. Record the selected release in project setup or CI rather than silently following the latest release.

### Mutation CLI

```text
crap-mutate --language csharp|go|typescript [options] [path ...]
crap-mutate mcp
```

Options:

| Option | Default | Purpose |
| --- | --- | --- |
| `--language NAME` | required | Select `csharp`, `go`, or `typescript`. |
| `--format text\|json` | `text` | Select findings-oriented text or versioned JSON. |
| `--minimum-score SCORE` | `80` | Set the accepted score from 0 through 100. |
| `--fail-on-threshold` | `false` | Exit with code `2` when the score is unavailable or below the minimum. |
| `--timeout DURATION` | `30m` | Stop the engine after a Go duration such as `10m` or `1h`. |
| `--incremental` | `false` | Enable StrykerJS incremental mode. |
| `--report-path PATH` | StrykerJS default | Read a custom StrykerJS JSON reporter path. |
| `--version` | | Print the wrapper version. |

Run from the directory where the native engine normally runs. Stryker.NET usually runs from the C# test project, Gremlins from the Go module root, and StrykerJS from the directory containing its configuration.

```sh
# C#: paths become repeated Stryker.NET --mutate options
crap-mutate --language csharp --minimum-score 80 --fail-on-threshold "../Example/**/*.cs"

# Go: run one package directory at a time
crap-mutate --language go --minimum-score 80 --fail-on-threshold ./internal/analysis

# TypeScript: paths become StrykerJS --mutate values
crap-mutate --language typescript --minimum-score 80 --fail-on-threshold "src/**/*.ts" "!src/**/*.spec.ts"
```

For Go, pass at most one package directory per run. The path must stay within the project root. Run the command once per package when a module has no Go package at its root; Gremlins otherwise scans recursively without collecting useful package coverage. Configure file exclusions in Gremlins itself. C# and TypeScript paths use each Stryker engine's glob syntax.

The text report prints survived and uncovered mutants. JSON includes every mutant and these fields:

- `scoreSource: "engine"` means the engine supplied the score directly. Gremlins supplies test efficacy as `KILLED / (KILLED + LIVED)`.
- `scoreSource: "report-statuses"` means the wrapper applied Stryker's mutation-score categories: `(Killed + Timeout) / (Killed + Timeout + Survived + NoCoverage)`.
- Compile errors, ignored mutants, and other non-scorable statuses remain in counts but not the Stryker score denominator.
- `passed` is true when a score exists and is greater than or equal to `minimumScore`.

StrykerJS writes JSON to `reports/mutation/mutation.json` by default. If `jsonReporter.fileName` changes that location, pass the same path with `--report-path`. This must identify a `.json` file inside the project root. The wrapper verifies that StrykerJS updated the report during the current run, but it does not delete the previous report. Stryker.NET and Gremlins reports are written to temporary locations and removed after normalization.

Native engine threshold exits are accepted only when the engine produced a current, valid report; `crap-mutate` then applies `minimumScore`. A timeout or cancellation always fails the run and terminates the native process tree. If Gremlins finds no mutants, the report has `score: null`, `scoreSource: "unavailable"`, zero mutants, and `passed: false`.

Exit codes match the CRAP CLI: `0` for a completed run, `1` for arguments or engine/report failure, and `2` for a requested threshold failure.

### Angular

Use StrykerJS's Angular setup and keep Angular's mutation ignorer enabled. A minimal production-source selection is:

```json
{
  "$schema": "./node_modules/@stryker-mutator/core/schema/stryker-schema.json",
  "mutate": ["src/**/*.ts", "!src/**/*.spec.ts", "!src/test.ts", "!src/environments/*.ts"],
  "ignorers": ["angular"],
  "reporters": ["json"]
}
```

Keep the test-runner section generated for the Angular project's Karma, Jest, or other supported setup. Then run:

```sh
crap-mutate --language typescript --minimum-score 80 --fail-on-threshold
```

### Mutation MCP Server

Start the separate mutation server with `crap-mutate mcp --root /absolute/path/to/project`. It exposes `run_mutation_tests` with these inputs:

| Input | Type | Default | Purpose |
| --- | --- | --- | --- |
| `root` | string | server `--root` | Existing project inside a configured authorized root. |
| `language` | string | required | `csharp`, `go`, or `typescript`. |
| `paths` | string array | Go root package | C# or TypeScript source paths/globs; required for authorized MCP runs. |
| `minimumScore` | number | `80` | Accepted score from 0 through 100. |
| `timeoutSeconds` | integer | `1800` | Maximum native engine runtime. |
| `incremental` | boolean | `false` | Enable StrykerJS incremental mode. |
| `reportPath` | string | StrykerJS default | Custom StrykerJS JSON report path inside `root`. |
| `resultMode` | string | `actionable` | Return `summary`, `actionable`, or `all` mutants. |
| `statuses` | string array | mode default | Override the mode with normalized status filters. |
| `limit` | integer | `20` | Return at most this many mutants; maximum `100`. |

Example MCP configuration:

```json
{
  "mcpServers": {
    "mutation": {
      "command": "/absolute/path/to/crap-mutate",
      "args": ["mcp", "--root", "/absolute/path/to/project"]
    }
  }
}
```

`run_mutation_tests` executes the engine once, retains an immutable normalized report for 30 minutes, and returns a compact first page plus `reportId`. The paging envelope has `pageSchemaVersion: "1"`; the nested `schemaVersion` remains the normalized mutation report contract. Actionable mode returns survived and uncovered mutants. Use the read-only `get_mutation_results` tool with `page.nextCursor` for continuation pages, or with `reportId` and a new mode/status filter to start another view. Snapshots are bounded and may be evicted; rerun the mutation tool when a report is expired or unavailable.

MCP root checks prevent caller-supplied paths and report locations from escaping configured roots, including through existing symlinks. Authorized mutation globs reject brace expansion and wildcard scopes containing symlinks. These checks are not a process sandbox or a defense against concurrent filesystem replacement: mutation engines, build scripts, and project tests execute with the server process's filesystem and network privileges. Run `crap-mutate mcp` only for trusted projects and accounts.

Example tool input:

```json
{
  "root": "C:\\source\\angular-app",
  "language": "typescript",
  "paths": ["src/**/*.ts", "!src/**/*.spec.ts"],
  "minimumScore": 80,
  "incremental": true,
  "timeoutSeconds": 3600
}
```

Both MCP servers publish initialization instructions that tell capable clients when to call their tool and forbid AI-estimated scores. These instructions are guidance, not enforcement; project-level agent rules remain the reliable way to require a tool call in a specific workflow.

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
go test -race ./internal/mutation ./internal/mutationmcp ./cmd/crap-mutate
go vet ./...
go build ./...
```
