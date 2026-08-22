# Releasing

Releases are created by pushing a `v*` tag. The workflow builds both `crap` and
`crap-mutate` natively because Tree-sitter requires CGO and a target-native C
compiler.

## Supported Artifacts

Each archive contains both executables, this project's `LICENSE`, and the
license and notice files supplied by every compiled Go module.

| Target | Archive |
| --- | --- |
| Linux amd64 | `crap_vVERSION_linux_amd64.tar.gz` |
| Linux arm64 | `crap_vVERSION_linux_arm64.tar.gz` |
| macOS amd64 | `crap_vVERSION_darwin_amd64.tar.gz` |
| macOS arm64 | `crap_vVERSION_darwin_arm64.tar.gz` |
| Windows amd64 | `crap_vVERSION_windows_amd64.zip` |

The workflow does not claim support for targets that are not built on a native
hosted runner.

## Prepare and Validate

1. Set `internal/buildinfo.Version` to the intended version without a `v`
   prefix and update `CHANGELOG.md`.
2. Run `gofmt -w` on changed Go files, then `go test ./...`, `go vet ./...`,
   and `go build ./...` with CGO enabled.
3. Validate native packaging locally with a full commit SHA:

   ```sh
   go run ./scripts/package --version 0.2.0 --revision FULL_40_CHARACTER_SHA --output dist
   ```

4. Inspect the archive, including its `LICENSE` and `licenses/` tree, run both
   binaries with `--version`, and create an
   annotated tag such as `git tag -a v0.2.0 -m "v0.2.0"`.
5. Push the tag only after its version exactly matches `buildinfo.Version` and
   its commit is on `main`. The release workflow tests the tagged commit and
   rejects version or ancestry mismatches.

The packager uses `-trimpath`, disables implicit VCS stamping, and injects these
exact variables with linker flags:

```text
github.com/hbaldwin98/crap/internal/buildinfo.Version
github.com/hbaldwin98/crap/internal/buildinfo.Revision
github.com/hbaldwin98/crap/internal/buildinfo.Modified
```

Archive metadata and file ordering are normalized. The release workflow pins
Go 1.25.8, but native C compilers and hosted-runner images can still change, so
release reruns are not claimed to be bit-for-bit reproducible.

## Verify a Release

Download all assets and verify their checksums:

```sh
sha256sum -c SHA256SUMS
```

Each archive has an SPDX JSON SBOM. GitHub also stores build-provenance and SBOM
attestations for each archive. With GitHub CLI installed, verify an archive:

```sh
gh attestation verify crap_v0.2.0_linux_amd64.tar.gz --repo hbaldwin98/crap
```

Inspect the matching `.spdx.json` file directly when auditing dependencies.

## Deferred Publication Metadata

Do not publish MCP Registry metadata until the first GitHub release exists and
the registry's current schema can identify the two stdio entry points without
ambiguity. At that point, validate metadata against the registry tooling and
reference immutable release assets rather than repository builds.

Native mutation-engine compatibility automation is also deferred. The
repository has parser and fake-runner contract tests, but no minimal C#, Go,
and TypeScript fixture projects that can execute Stryker.NET, Gremlins, and
StrykerJS without false compatibility claims. Add a scheduled workflow only
after those fixtures exist and every engine/toolchain version is pinned.
