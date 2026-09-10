# CLI builds and releases

The CLI is an independent Go module. Local builds and GitHub Actions use the
same `cli/build.sh` entrypoint and Go distribution helper; GoReleaser is not
required. CLI release builds use Go 1.25.13 and run a vulnerability check before
packaging. This patch release fixes standard-library findings reached by the
development supervisor; use the same toolchain when reproducing release assets.

The next planned CLI release is **`v0.1.1-dev`**, with Git tag
**`cli/v0.1.1-dev`**. Only the CLI is in scope for publication; no separate
server tag or server Release is planned. GitHub treats this CLI version as a
prerelease. The current pipeline publishes its binary archives and verifies
the exact Go installation version, but does not update the default Homebrew tap.

After this version has been published:

```bash
go install github.com/xgtian-root/aginex/cli/cmd/aginex@v0.1.1-dev
```

## Local builds

From the repository root:

```bash
./cli/build.sh
./cli/dist/aginex --version
./cli/build.sh --all
```

The script resolves its own location, so an absolute path works from another
directory, including paths containing spaces. Run it with Bash on macOS or
Linux, or Git Bash on Windows. `--help` lists options. A native Windows build
produces `cli/dist/aginex.exe`. No root privileges or Node toolchain are needed
to build or execute `--help`, `--version`, or `new`; development commands still
require the application's toolchain.

`--all` builds darwin/linux/windows × amd64/arm64. Each archive contains the
executable, `LICENSE`, and `NOTICE`. Unix archives are tar.gz; Windows archives
are zip. For example, a release includes:

```text
cli/dist/aginex_0.1.1-dev_darwin_arm64.tar.gz
cli/dist/aginex_0.1.1-dev_windows_amd64.zip
cli/dist/SHA256SUMS
cli/dist/release.json
```

Stable release builds additionally include `aginex.rb`; `v0.1.1-dev` does not.

`release.json` records the CLI version, full commit, commit timestamp, bundled
backend version, platform names, and archive hashes. `SHA256SUMS` covers all
six archives. `--dist /absolute/output/path` overrides the output directory.
Only the files listed for that manifest are published; unrelated older output
files are not release assets. Build outputs are ignored by Git.

Builds disable CGO and workspace resolution, use readonly module dependencies,
and strip local source paths. Version, commit, and timestamp are injected at
link time. Archive timestamps use the Git commit time rather than the current
clock, allowing reruns with the same source and toolchain to compare bytes.
Development builds retain a development version and mark dirty commits.

Every build verifies the embedded scaffold against canonical source files.
When the check reports drift, review the differences and synchronize explicitly:

```bash
go run ./cli/cmd/sync-templates
go run ./cli/cmd/sync-templates -check
```

The sync tool rejects manually modified snapshot files. Reconcile those changes
with their canonical sources before rerunning it; never edit the snapshot as
the primary source.

## One-time Homebrew setup

1. Create the public `xgtian-root/homebrew-tap` repository with an initial commit
   and default branch. For example, after authorizing repository creation:

   ```bash
   gh repo create xgtian-root/homebrew-tap --public --add-readme
   ```

2. Create a fine-grained personal access token limited to that repository, with
   **Contents: Read and write**. Add it to the Aginex repository as the Actions
   secret `HOMEBREW_TAP_TOKEN`. Do not put a token in source files or shell history:

   ```bash
   gh secret set HOMEBREW_TAP_TOKEN --repo xgtian-root/aginex
   ```

3. Enable Actions on Aginex. The release job uses its job-scoped `GITHUB_TOKEN`
   for Releases; only the tap job receives the cross-repository token. The tap
   default branch must allow this token to update `Formula/aginex.rb` through
   the Contents API. A policy requiring pull requests will block this direct
   automatic update and must be accounted for when configuring the tap.

The workflow does not create the tap, issue credentials, create Git tags, or
publish backend versions. The setup commands above are explicit one-time
administrative operations, separate from building the CLI.

## Preparing a release

The release target is CLI `v0.1.1-dev`. Generated projects use the
server Go module dependency, pinned in `cli/templates/assets.go` to source
commit `f6d57f4fe47fb451c35d19185a32a5504bff67a9` through pseudo-version
`v0.0.0-20260910062837-f6d57f4fe47f`. No separate server tag or Release is needed.

This source pin includes the configuration protocol required by the new CLI.
For future backend changes, push the new source commit, resolve its server
pseudo-version with Go, and update `BackendVersion` and the source reference
above before publishing. Changing the CLI version alone is not enough.

1. Push the reviewed source commits so Go can download the pinned framework.
2. Verify the pin with `go list -m -json` or `go mod download -json` outside the
   source workspace. For future framework updates, resolve the new source commit
   with Go and update `BackendVersion` before committing the CLI release.
3. Run CLI tests, vet, and scaffold checks; review and commit the release changes.
4. Create `cli/v0.1.1-dev` on the clean commit, validate the release build, then
   push that CLI tag to start automatic publication.

Release builds verify that the pinned framework can be downloaded. Unpinned
development declarations are rejected; source-commit pseudo-versions are valid
for both stable and prerelease CLIs. The CLI version does not change the
installation configuration version.

To validate and package an existing tag locally, without publishing:

```bash
./cli/build.sh --release v0.1.1-dev
GOWORK=off go -C cli run ./cmd/release verify
GOWORK=off go -C cli run ./cmd/release smoke
```

Use a full canonical `v0.x.y` or `v1.x.y` version, optionally with a prerelease
suffix. Major version 2 requires a separate Go module-path migration and is
not implicitly supported. Version build metadata and abbreviated versions
are rejected. HEAD must match the tag exactly, and the worktree must be clean.

## Automatic publication

Pushing the selected `cli/vX.Y.Z` tag triggers `CLI build and release`:

1. Test/vet the independent CLI module, verify the scaffold and backend download,
   and build all six archives with the pinned toolchain.
2. Verify archive checksums, binary formats and architectures, and execute
   native binaries on macOS, Linux, and Windows runners. `new` must render a
   project with the intended backend version from the embedded templates.
3. Create a draft GitHub Release, upload all verified assets, then publish it.
   Existing assets are downloaded and compared before any upload on a retry.
4. Install the exact version using `go install ...@vX.Y.Z`, check its reported
   version, and create a project outside the source checkout. Download that
   project's complete dependency graph with `go mod download all` and compile
   its backend. Go proxy delays
   receive bounded retries; persistent failure leaves the job failed.
5. For stable versions, install and test the release's Homebrew formula in
   temporary CI taps on macOS and Linux, then update the public tap.

Prereleases are marked as such in GitHub Releases and do not update the default
Homebrew formula. Install them from an archive or with an explicit Go version.
Go installation reads the downloaded module version from build metadata; local
source builds without release stamping retain the development identifier.

The Homebrew formula selects the matching macOS/Linux binary and SHA-256 hash.
It installs the binary directly and does not require Go just to install the CLI.
It includes `--version` and `--help` tests. Windows users use an archive or Go.

## Retry and failure behavior

- A retry reuses byte-identical release assets. It never overwrites differing
  files or adds missing assets to an already public release. Keep the original
  toolchain and source for a full retry; publish a new version for changed bytes.
- A draft can resume missing uploads. The public release step happens only after
  all required files are present and existing files have matched.
- A failed Go installation or Homebrew check leaves the GitHub Release available
  and prevents the tap update. Publishing the Git tag already makes the module
  discoverable by Go; workflow failure does not retract or roll back that tag.
- Tap writes are serialized and use the current file SHA. Older version retries
  cannot downgrade the formula. Existing generated formulas are checked against
  their previous release manifest; unrecognized files and manual edits require
  reconciliation instead of being overwritten.

Use **Actions → CLI build and release → Run workflow**, enter the existing full
tag, and enable **tap_only** to retry installation checks and the tap update
without rebuilding or republishing binaries. Keep it disabled for a full retry.
Both modes use source from the selected tag.

For diagnosis, the helper supports `verify`, `smoke`, `publish`, `verify-install`,
and `tap`. Only `publish` and `tap` write to GitHub and require appropriately
authorized credentials; normal builds and tests never call them automatically.

## Verification boundaries

`go -C cli test ./...` covers version resolution, tag/dirty-tree checks, archive
contents and deterministic packaging, manifest validation, publication retries,
formula drift/downgrade protection, and an isolated local module-proxy installation.
The proxy fixture uses a temporary module cache and never publishes a test version.
`go -C cli test -short ./...` omits only that standalone installation test.

Use `./cli/build.sh --all` and the `smoke` helper to validate real binaries locally.
Cross-compilation and format checks prove artifact targets; only the host binary
runs locally. The Actions matrix adds native OS and Homebrew installation checks.
Actual GitHub uploads, public Go proxy propagation, and the configured tap token
remain unverified until a real release workflow succeeds.
