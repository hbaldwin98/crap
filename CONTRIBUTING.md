# Contributing

## Before Opening a Change

Use a GitHub issue for significant behavior changes. Security reports belong
in the private process described in [SECURITY.md](SECURITY.md).

## Development

Install Go 1.25 or newer and a C compiler for Tree-sitter's CGO bindings. Run:

```sh
go test ./...
go test -race ./internal/mutation ./internal/mutationmcp ./cmd/crap-mutate
go vet ./...
go build ./...
```

Format Go changes with `gofmt`. Add tests for behavior changes and update the
published schemas and golden fixtures when changing a report contract.

## Pull Requests

Keep changes focused, explain user-visible effects, and note checks that could
not be run. By submitting a contribution, you agree that it is licensed under
the repository's MIT License.
