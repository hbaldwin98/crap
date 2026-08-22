# crap

[![CI](https://github.com/hbaldwin98/crap/actions/workflows/ci.yml/badge.svg)](https://github.com/hbaldwin98/crap/actions/workflows/ci.yml)
[![CodeQL](https://github.com/hbaldwin98/crap/actions/workflows/codeql.yml/badge.svg)](https://github.com/hbaldwin98/crap/actions/workflows/codeql.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

This repository contains two tools:

- `crap` deterministically calculates cyclomatic complexity and CRAP scores for C#, Go, TypeScript, and TSX callables.
- `crap-mutate` runs a language-native mutation engine and converts its output into one stable report for C#, Go, and TypeScript.

Both tools can run from the command line or as separate MCP stdio servers.

[Changelog](CHANGELOG.md) | [Contributing](CONTRIBUTING.md) | [Security](SECURITY.md) | [Support](SUPPORT.md) | [Releasing](docs/releasing.md)

The tools parse native reports or source and calculate every score. An AI caller can choose paths, coverage, a Git diff, and score thresholds, but it does not derive or reinterpret results.

## Requirements

- Go 1.25 or newer
- A C compiler available to Go, required by Tree-sitter's CGO bindings

On Windows, install a GCC or Clang toolchain and ensure its compiler is on `PATH`. On macOS, install the Xcode command-line tools. Most Linux development environments provide GCC through their package manager.

## Quick Start

Install both binaries and configure every detected supported MCP client with one command:

```sh
go run github.com/hbaldwin98/crap/cmd/crap-install@latest
```

The installer requires Go 1.25 or newer and a C compiler available to Go for Tree-sitter's CGO bindings. It installs `crap` and `crap-mutate` into `GOBIN`, or the first `GOPATH` entry's `bin` directory when `GOBIN` is empty. MCP configurations use absolute executable paths, so that directory does not need to be on the MCP client's `PATH`.

By default, the installer configures Claude Code and OpenCode when their executables are detected. It always writes a client-neutral MCP reference to `~/.config/crap/mcp.json` and prints that path, including when no supported client is detected. Re-running the command updates the same binaries and managed config entries.

Select clients explicitly with repeatable or comma-separated `--client` flags:

```sh
go run github.com/hbaldwin98/crap/cmd/crap-install@latest --client claude,opencode
go run github.com/hbaldwin98/crap/cmd/crap-install@latest --client generic
go run github.com/hbaldwin98/crap/cmd/crap-install@latest --client claude --client opencode --dry-run
```

Supported names are `claude`, `opencode`, and `generic`. Explicit `claude` selection requires the `claude` CLI. `--dry-run` prints commands and config paths without installing or writing files. `--version VERSION` installs both commands from the same validated module version and defaults to `latest`.

OpenCode honors `OPENCODE_CONFIG`, `OPENCODE_CONFIG_DIR`, and `XDG_CONFIG_HOME`. `OPENCODE_CONFIG` names one exact file. Without that override, the installer updates both `opencode.json` and `opencode.jsonc` when both exist because OpenCode loads both in that order. If neither exists, it creates `~/.config/opencode/opencode.json`. Existing unrelated settings, MCP servers, and JSONC comments are preserved. The two managed server definitions are replaced, including comments inside those definitions.

The generic config receives the same targeted update: unrelated top-level properties, MCP servers, and JSONC comments are preserved, while comments inside the two managed definitions are replaced. The installer rejects OpenCode and generic destination symlinks before installing anything. Safe replacement preserves ordinary existing file mode bits, but not ownership, ACLs, extended attributes, or other filesystem metadata. Newly created config files use mode `0600` where the operating system supports POSIX modes.

Claude Code is configured only through `claude mcp add`. If a user-scoped `crap` or `crap-mutate` server already exists, the installer leaves it unchanged rather than removing it. Because install paths are stable, this is normally the desired idempotent result. If an existing definition points elsewhere, remove it manually with `claude mcp remove --scope user NAME`, then rerun the installer.

Before installation, the command parses and renders every config target and checks `go env GOOS` and `go env GOARCH` against the running installer. This catches malformed configs, unsafe destinations, and persistent `GOENV` cross-compilation settings before `go install`. Immediately before writing a prepared config, the installer also verifies that the file is still byte-for-byte identical to its preflight state, or is still absent if it did not exist. Concurrent changes are preserved and reported with instructions to rerun. A transaction cannot span Go installation, filesystem writes, and external client CLIs. If a later step fails, rerun the command after correcting the error; completed earlier steps are idempotent and are printed as they finish.

To build manually instead, run these commands from the repository root:

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
| `--format text\|json\|sarif` | `text` | Select human-readable, versioned JSON, or SARIF 2.1.0 output. |
| `--output PATH` | stdout | Write the report using safe same-directory replacement instead of stdout. |
| `--coverage PATH` | none | Read Cobertura XML or a native Go coverprofile. |
| `--diff-base REVISION` | none | Return only callables touching lines changed from a Git revision. |
| `--threshold SCORE` | `30` | Mark scores strictly greater than this value as above threshold. |
| `--fail-on-threshold` | `false` | Exit with code `2` when any returned callable is above threshold. |
| `--include-tests` | `false` | Include Go `_test.go` and TypeScript `.spec`/`.test` files. |
| `--include-generated` | `false` | Include recognized generated C# and TypeScript files. |
| `--exclude PATTERN` | none | Exclude a root-relative gitignore-style pattern; repeat as needed. |
| `--strict-coverage` | `false` | Fail when a supplied coverage report has unmatched or ambiguous source paths. |
| `--version` | | Print the version. |
| `-h`, `--help` | | Print help and exit successfully. |

The standard Go flag rules apply: options must precede the first path, and an unknown option in that position is an error. Parsing stops at the first path, so later option-looking values are paths. Use `--` to end option parsing explicitly, especially before a path that starts with `-`. Paths are resolved from the current working directory and can be individual files or directories.

`--output` creates a temporary file beside the destination and replaces the destination only after the complete report has been rendered, synced, and closed. Existing POSIX permission bits are copied where supported; ownership, ACLs, and other filesystem metadata are not preserved. Destination symlinks, including dangling symlinks, are rejected. Unix uses same-directory rename followed by directory sync; Windows uses `MoveFileEx` with replace-existing and write-through flags. A render, temporary-file sync, or close failure leaves an existing destination unchanged. Filesystem-specific replacement guarantees still apply. When `--output` is set, stdout remains empty; diagnostics still go to stderr.

Analyze selected paths with a maximum allowed score of 20:

```sh
./crap --threshold 20 --fail-on-threshold ./internal ./cmd
```

The threshold does not change or cap calculated scores. It sets `aboveThreshold` on each result and controls `--fail-on-threshold`. A score equal to the threshold passes; a score greater than it fails.

### Source Discovery

Directory analysis honors repository `.gitignore` files and an optional root `.crapignore`, both with gitignore pattern semantics. `--exclude` adds non-negated root-relative exclusions. Explicitly named source files override `.gitignore`, `.crapignore`, generated-file, and test-file defaults, but not `--exclude`; explicitly named unsupported files fail instead of disappearing silently.

Built-in directory exclusions cover `.git`, `node_modules`, `.next`, `bin`, `obj`, and Go-style `testdata` directories at any depth, plus root `vendor`, `dist`, `build`, and `coverage`. An explicitly named source file remains selectable even inside one of these directories. Generated files such as `*.g.cs`, `*.generated.cs`, `*.designer.cs`, `*.d.ts`, and `*.generated.ts` are excluded unless `--include-generated` is set. JSON reports include a compact `discovery` section with the selected count and deterministic exclusion counts/examples.

Exit codes:

| Code | Meaning |
| --- | --- |
| `0` | Analysis succeeded and no requested threshold failure occurred. |
| `1` | Arguments, source parsing, coverage, Git, or output failed. |
| `2` | Analysis succeeded, but at least one score exceeded the threshold while `--fail-on-threshold` was set. |

### SARIF and GitHub Code Scanning

`--format sarif` emits deterministic SARIF 2.1.0 with one `CRAP001` result per callable above the threshold. GitHub locations use root-relative escaped slash URIs and 1-based UTF-16 code-unit columns converted from the analysis report's UTF-8 byte columns. Every result has an explicit start and exclusive end. Result properties contain the CRAP score, complexity, coverage, and threshold. `partialFingerprints.primaryLocationLineHash` and a tool-specific fingerprint contain the stable callable ID so same-line callables remain distinct.

SARIF rendering validates each location against the current canonical root-relative source file and fails rather than emitting stale or invalid coordinates. GitHub accepts at most 25,000 results per upload; the command returns an output error instead of truncating when that limit would be exceeded.

Generate a file for GitHub code scanning with:

```sh
crap --format sarif --output crap.sarif --threshold 20 .
```

Upload `crap.sarif` with GitHub's `github/codeql-action/upload-sarif` action. SARIF is the supported GitHub integration; the CLI does not emit a separate annotation format.

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

Cobertura coverage uses line-hit records. Go coverprofiles use their native statement counts. Callable names and ranges always come from the C#, Go, or TypeScript AST; Cobertura method names are ignored because instrumentation and source-map processing can rewrite them. A nested callable owns its own lines, so its coverage is excluded from its parent.

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

The server exposes `analyze_code` and `get_analysis_results`. `analyze_code` runs one analysis, stores an immutable serialized snapshot, and returns its first page. Its inputs are:

| Input | Type | Default | Purpose |
| --- | --- | --- | --- |
| `root` | string | server `--root` | Select an existing project inside a configured `--root` or `--allow-root`. |
| `paths` | string array | `root` | Analyze these files or directories, relative to `root`. |
| `coveragePath` | string | none | Read Cobertura XML or a Go coverprofile, relative to `root`. |
| `diffBase` | string | none | Return only callables changed from this Git revision. |
| `crapThreshold` | number | `30` | Mark scores strictly greater than this value as above threshold. |
| `includeTests` | boolean | `false` | Include Go `_test.go` and TypeScript `.spec`/`.test` files. |
| `includeGenerated` | boolean | `false` | Include recognized generated C# and TypeScript files. |
| `exclude` | string array | none | Add root-relative gitignore-style exclusions; negated entries are rejected. |
| `strictCoverage` | boolean | `false` | Fail when supplied coverage paths are unmatched or ambiguous. |
| `resultMode` | string | `violations` | Return `summary`, `violations`, `highest`, or `all` methods. |
| `limit` | integer | `20` | Return at most this many methods; maximum `100`. |
| `offset` | integer | `0` | Deprecated initial-page offset retained for compatibility; prefer continuation cursors. |

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

Every response includes `reportId`, `expiresAt`, the full analysis summary, coverage diagnostics, and a `page` object. Methods are sorted by descending CRAP score before pagination. `violations` returns only methods above the requested threshold, `highest` and `all` return all methods, and `summary` returns no methods. When `page.nextCursor` is present, pass it by itself to `get_analysis_results`. A cursor is signed and bound to its report, result mode, offset, and limit. Alternatively, the retrieval tool accepts `reportId` with a result mode and limit to start that view from its first page. The legacy `analyze_code.offset` and `page.nextOffset` fields remain available, but cursors are preferred because they continue the same immutable snapshot. Snapshot pages never rerun analysis and remain unchanged if source or coverage files are modified or deleted. Snapshots expire at `expiresAt` and may be evicted earlier by bounded server storage.

Check `summary.aboveThreshold` for the violation count, `summary.maximumCrap` for the highest actual score, and each returned method's `aboveThreshold` field. Use the CLI JSON format when one complete unpaged report is required. The MCP server returns findings rather than a process exit code.

The analysis CLI emits analysis report schema v6. The current MCP envelope uses `pageSchemaVersion: "4"` and `reportType: "analysis-page"`; historical page schemas v1 through v3 remain published. Canceling an analysis request stops discovery, coverage and Git work, parsing, file dispatch, and initial-page construction as soon as practical. Cancellation and deadline errors observed before snapshot insertion are preserved without returning a partial report. Snapshot insertion is the final commit point; for the same inputs and project state, `analyze_code` retains idempotent analysis semantics even though each successful call receives a new report ID.

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
crap-mutate doctor --language csharp|go|typescript [options] [path ...]
crap-mutate mcp
```

Options:

| Option | Default | Purpose |
| --- | --- | --- |
| `--language NAME` | required | Select `csharp`, `go`, or `typescript`. |
| `--format text\|json\|sarif` | `text` | Select findings-oriented text, versioned JSON, or SARIF 2.1.0. |
| `--output PATH` | stdout | Write the report using safe same-directory replacement instead of stdout. |
| `--minimum-score SCORE` | `80` | Set the accepted score from 0 through 100. |
| `--fail-on-threshold` | `false` | Exit with code `2` when the score is unavailable or below the minimum. |
| `--timeout DURATION` | `30m` | Stop the engine after a Go duration such as `10m` or `1h`. |
| `--incremental` | `false` | Enable StrykerJS incremental mode. |
| `--report-path PATH` | StrykerJS default | Read a custom StrykerJS JSON reporter path. |
| `--dry-run` | `false` | Validate inputs and print the exact native command plan without executing it. |
| `--version` | | Print the wrapper version. |
| `-h`, `--help` | | Print help and exit successfully. |

Options must precede the first path. Parsing stops at that path, so later option-looking values are paths; use `--` to end option parsing explicitly or before a path starting with `-`. Run from the directory where the native engine normally runs. Stryker.NET usually runs from the C# test project, Gremlins from the Go module root, and StrykerJS from the directory containing its configuration.

For completed mutation runs, `--format sarif` emits deterministic SARIF 2.1.0 containing actionable survived (`MUT001`) and uncovered (`MUT002`) mutants. GitHub locations are 1-based UTF-16 code units with explicit starts and ends. StrykerJS and Stryker.NET ranges are treated as native one-based UTF-16 coordinates and validated against source; zero-length native ranges are highlighted as one source character. Gremlins point columns are converted from Go byte columns and receive a one-character SARIF-only range; normalized mutation reports and IDs remain unchanged. Sources must be existing regular root-relative files and cannot traverse symlinks. `partialFingerprints.primaryLocationLineHash` and a tool-specific fingerprint contain the stable normalized mutant ID. Engine, status, and minimum-score properties are included. Rendering more than 25,000 actionable results fails instead of truncating. `doctor` and `--dry-run` do not accept SARIF because they do not produce mutation findings. Use `--output mutation.sarif` and GitHub's SARIF upload action for code scanning.

Run `crap-mutate doctor --language NAME [path ...]` before a first mutation run or after changing engine versions. Doctor checks the executable, a language-specific project marker, and the native version command without running mutation tests. It states the native report contract enforced later when a run is parsed: Gremlins v0.6 JSON, Stryker.NET schema 2, or StrykerJS schema 1.0. The version probe alone does not prove report compatibility. A missing project marker or unverified compatibility is a warning; a missing or failing engine is an error. Add `--format json` for automation.

`--dry-run` returns schema-versioned plan JSON or text. The argument list is the same one execution consumes. `$REPORT_PATH` marks a private temporary report created only during Gremlins and Stryker.NET execution; StrykerJS plans show the configured in-project report path.

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

`run_mutation_tests` executes the engine once, retains an immutable normalized report for 30 minutes, and returns a compact first page plus `reportId`. The paging envelope has `pageSchemaVersion: "2"` and `reportType: "mutation-page"`; the nested `schemaVersion` identifies mutation report v3. Actionable mode returns survived and uncovered mutants. Use the read-only `get_mutation_results` tool with `page.nextCursor` for continuation pages, or with `reportId` and a new mode/status filter to start another view. Snapshots are bounded and may be evicted; rerun the mutation tool when a report is expired or unavailable.

The read-only `plan_mutation_run` tool validates the same authorized inputs and returns the native command plan without execution. `check_mutation_setup` additionally executes the native version probe and returns project, executable, version, and report-contract checks. Because a project-local version command can execute project code, clients must not treat this tool as read-only or auto-approve it. These tools accept the setup fields through `reportPath`; paging fields apply only to `run_mutation_tests` and `get_mutation_results`.

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

## Report Contracts

JSON outputs carry `reportType`, `schemaVersion`, one shared tool version, coordinate semantics, and deterministic fingerprints. Analysis is v6, mutation is v3, mutation plans are v2, and mutation doctor output has its independent v1 contract. Incompatible changes increment the contract that changed; MCP page versions are independent from their underlying report versions. Analysis v5 added Go function literals and C# lambdas, anonymous methods, and expression-bodied properties/indexers. Analysis v6 adds deterministic source-discovery policy and metadata.

Normalized analysis coordinates are 1-based UTF-8 byte columns with exclusive ends. Normalized mutation coordinates remain engine-native. SARIF is a separate presentation contract and converts or validates both forms as 1-based UTF-16 code units for GitHub. Analysis callable names, signatures, and ranges come from the language AST. Callable IDs hash language, normalized file path, kind, lexical signature, and same-signature occurrence, so inserting blank lines above a callable does not change its ID. Mutation wrapper IDs hash normalized file, range, mutator, and replacement; Stryker's `nativeId` and status do not affect them.

`fingerprints.sources` contains normalized displayed paths and exact source-byte SHA-256 digests. Coverage metadata identifies `none`, `cobertura`, or `go-coverprofile` and can include the original displayed path and exact-byte digest. Fingerprints also cover the native mutation report, resolved Git commits when changed analysis is used, and semantic options. Absolute checkout roots and temporary report paths are not serialized into fingerprints. No timestamp is added to deterministic CLI reports; MCP mutation snapshot expiry remains envelope metadata.

Published JSON Schema 2020-12 files are under [`schemas/v1`](schemas/v1), with matching golden examples under [`testdata/contracts`](testdata/contracts). The schemas reject unknown properties and constrain versions, enums, bounds, nullability, and SHA-256 formats.

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

Complexity starts at `1` for each callable. Every listed branch adds exactly `1`, including each logical operator in a compound expression. Branches inside a nested callable belong only to that nested callable.

C# callables are methods, constructors, destructors, operators, local functions, accessors, lambdas, anonymous methods, and expression-bodied properties and indexers. C# adds one for each `if`, `for`, `foreach`, `while`, `do`, `catch`, switch expression arm, ternary, `and`/`or` pattern, and `&&`, `||`, or `??` expression. Traditional switch statement labels do not add complexity. Nested callables are scored separately.

The bundled C# grammar supports C# 1 through C# 13. The analyzer rejects syntax errors rather than silently producing a partial score.

Go callables are functions, methods, and function literals. Go adds one for each `if`, `for`, non-default expression/type/communication `case`, and `&&` or `||` expression. Nested function literals are scored separately.

TypeScript and TSX callables are functions, generator functions, methods, function expressions, generator function expressions, and arrow functions. They add one for each `if`, `for`, `for in`/`for of`, `while`, `do`, `catch`, non-default `case`, ternary, and `&&`, `||`, or `??` expression. Nested callables are scored separately.

Files and callables are sorted by normalized path and source line. JSON contains no timestamps or environment-dependent IDs. Invalid syntax fails analysis instead of returning a partial score.

## Development

Run the automated checks from the repository root:

```sh
go test ./...
go test -race ./internal/mutation ./internal/mutationmcp ./cmd/crap-mutate
go vet ./...
go build ./...
go test -coverpkg=./... -coverprofile=coverage.out ./...
go run ./cmd/crap --coverage coverage.out --threshold 30 --fail-on-threshold .
```

CI runs the same coverage-backed CRAP gate and fails when any production callable scores above `30`.
