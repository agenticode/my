# Releases

A release of MemorY is a set of files attached to a GitHub release of a version tag:

| File | Content |
| --- | --- |
| `my_<version>_linux_amd64.tar.gz` | the `my` binary for Linux on x86-64, `LICENSE`, `README.md` |
| `my_<version>_linux_arm64.tar.gz` | the same for Linux on 64-bit ARM |
| `my_<version>_darwin_arm64.tar.gz` | the same for macOS on Apple silicon, for opening case bundles and writing reports on a workstation |
| `SHA256SUMS` | the SHA-256 checksum of each archive, in the form `sha256sum -c` reads |

The binary is pure Go, built with `CGO_ENABLED=0`, and statically linked. It needs no library and no configuration file on the host.

## What a binary says about itself

```
$ my version
my v1.0.0 (0123456789ab, 2026-11-16) go1.26.11 linux/amd64
```

The line holds the version, the commit the binary was built from, the day of that commit, the Go version and the platform. The commit above is a placeholder. `my version --json` prints the same and the versions of what the binary reads and writes: the analysis engine, the ledger schema, the case bundle format and the `--json` output.

A release build is stamped by `tools/dist`. A build with `make build` is stamped with what `git describe` says. A build with `go build` or `go install` takes what the Go toolchain recorded: the module version, the revision and its time; a working tree with changes is named `<revision>-dirty`.

## Reproducible archives

The same commit, built with the same Go version, gives the same archives byte for byte on any machine. The `toolchain` line of `go.mod` names the Go version of the release builds and of CI (go1.26.11 for 1.0.0); the go command downloads it when the installed one is older, and uses an installed newer one as it is. What makes the archives the same:

- the binary is built with `-trimpath`, without a build ID and without the version control stamp of the toolchain, for the baseline instruction set of each architecture;
- the version, the commit and the time of the commit are the only values stamped into it;
- the archive is written by `tools/dist`: members in a fixed order, the time of the commit as their time, owner and group 0 without names, no file name and no time in the gzip header. GNU tar and BSD tar write different bytes for the same files, so neither is used.

`make dist-check` builds the archives twice, once from the working tree and once from a copy of it in another directory with an empty build cache, and compares the checksums. CI runs it for every change. On 2026-11-15 one commit was also built on two machines with Go 1.26.0, a Linux x86-64 CI runner and a macOS arm64 machine: the three archives had the same checksums on both.

To check a published release against its source:

```
git checkout <version>
export GOTOOLCHAIN=go1.26.11    # the Go version "my version" of the release prints
make dist VERSION=<version>
diff dist/SHA256SUMS <the SHA256SUMS of the release>
```

## Making a release

1. Choose the version: `vMAJOR.MINOR.PATCH`. The workflow rejects a tag of another form.
2. Write its entry in [CHANGELOG.md](../CHANGELOG.md), headed `## [MAJOR.MINOR.PATCH] - YYYY-MM-DD`, and merge it. `.github/scripts/release-notes.sh <version>` prints the notes the release will carry, with links to files of the repository at the tag.
3. Try it without publishing. Either run the `release` workflow by hand from the Actions page (it builds, checks and keeps the archives with the run, and publishes nothing), or locally:
   ```
   make lint test
   make dist-check
   .github/scripts/check-dist.sh <version>
   ```
4. Tag the commit on `main` and push the tag:
   ```
   git tag -a <version> -m "MemorY <version>"
   git push origin <version>
   ```
5. The `release` workflow runs the tests, builds the archives twice and compares them, unpacks them, runs the binary, and creates the GitHub release "MemorY MAJOR.MINOR.PATCH" with the archives, `SHA256SUMS` and the notes of step 2.

A release can also be published from a local build, before the workflow reaches its last step:

```
make dist VERSION=<version>
.github/scripts/release-notes.sh <version> > notes.md
gh release create <version> dist/my_<version>_*.tar.gz dist/SHA256SUMS --verify-tag \
  --title "MemorY MAJOR.MINOR.PATCH" --notes-file notes.md
```

The workflow then leaves the release as it is and compares checksums: it fails when the `SHA256SUMS` of its own build differs from the published one. Such a release has been built on two machines, the local one and a Linux x86-64 runner.

A release that turns out wrong is not replaced: its files keep the checksums users may have recorded. Fix the problem and release the next patch version.

## Targets

| Command | What it does |
| --- | --- |
| `make build` | `bin/my` for this machine |
| `make cross` | `dist/my-<os>-<arch>` binaries for the three platforms, without archives |
| `make dist` | the archives and `SHA256SUMS` in `dist/`; `VERSION=` names the version, else `git describe` does |
| `make dist-check` | two builds from scratch, compared |
