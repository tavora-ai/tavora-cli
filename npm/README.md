# @tavora/cli

The Tavora CLI distributed as an npm package. Wraps the Go binary built from
`tavora-cli/cmd/tavora/`. Standard pattern: `postinstall` downloads the
prebuilt binary for the user's platform from a GitHub Release, and a JS shim
in `bin/tavora.js` execs it with the user's argv.

## Install

```sh
npm i -g @tavora/cli
# or
pnpm add -g @tavora/cli
# or
yarn global add @tavora/cli
```

## What ships in the package

| File | Purpose |
|---|---|
| `package.json` | Declares the `tavora` bin entry + `postinstall` hook |
| `install.js` | Postinstall — picks the platform artifact, downloads + ungzips into `bin/` |
| `bin/tavora.js` | JS shim — npm's `bin` target. Execs the platform binary, forwards stdio + signals |
| `bin/tavora` (or `tavora.exe`) | The Go binary, populated by `install.js` at install time |

## Supported platforms

| Platform / arch | Release artifact |
|---|---|
| darwin / arm64 | `tavora-darwin-arm64.gz` |
| darwin / x64 | `tavora-darwin-amd64.gz` |
| linux / arm64 | `tavora-linux-arm64.gz` |
| linux / x64 | `tavora-linux-amd64.gz` |
| win32 / x64 | `tavora-windows-amd64.exe.gz` |

Unsupported platforms get a clear "no prebuilt binary" message during install.

## Skipping the download

For monorepo or offline installs where the postinstall network hop isn't
viable:

```sh
TAVORA_SKIP_DOWNLOAD=1 npm i -g @tavora/cli
```

The package installs without a binary; the user is expected to drop one at
`node_modules/@tavora/cli/bin/tavora` (or `tavora.exe`) manually.

## Releasing

Publishing a new version requires both an npm publish *and* a matching GitHub
Release with the platform binaries attached. The CLI version in `package.json`
and the release tag must agree — the postinstall hardcodes
`v${pkg.version}` into the download URL.

Cross-compile the Go binary for each target:

```sh
# from tavora-cli/
mkdir -p npm/dist

# darwin (universal-ish — separate arm64 + amd64)
GOOS=darwin GOARCH=arm64 go build -o npm/dist/tavora-darwin-arm64 ./cmd/tavora
GOOS=darwin GOARCH=amd64 go build -o npm/dist/tavora-darwin-amd64 ./cmd/tavora

# linux
GOOS=linux GOARCH=arm64 go build -o npm/dist/tavora-linux-arm64 ./cmd/tavora
GOOS=linux GOARCH=amd64 go build -o npm/dist/tavora-linux-amd64 ./cmd/tavora

# windows
GOOS=windows GOARCH=amd64 go build -o npm/dist/tavora-windows-amd64.exe ./cmd/tavora

# gzip each (npm install.js gunzips the artifact directly)
gzip -9 npm/dist/tavora-darwin-arm64
gzip -9 npm/dist/tavora-darwin-amd64
gzip -9 npm/dist/tavora-linux-arm64
gzip -9 npm/dist/tavora-linux-amd64
gzip -9 npm/dist/tavora-windows-amd64.exe
```

Then publish:

```sh
# 1. Tag the repo + push, e.g. v0.0.1
git tag v0.0.1
git push --tags

# 2. Upload the gzipped artifacts to the GitHub Release for that tag
gh release create v0.0.1 npm/dist/*.gz --title "v0.0.1" --notes "..."

# 3. Publish the npm package
cd npm
npm publish --access public
```

The npm publish must happen *after* the GitHub Release exists, or the
postinstall download will 404 for users on the bleeding edge.

A CI workflow that does all of the above on tag push is the natural
follow-up; deferred until the release cadence justifies it.

## Migration path: per-platform optional dependencies

The current single-package + postinstall-download approach is the simplest
to ship. Once install reliability or offline support becomes important,
migrate to the esbuild / prisma pattern:

- Publish one platform-specific package per target (`@tavora/cli-darwin-arm64`,
  `@tavora/cli-linux-x64`, …) — each containing only the binary.
- Use `optionalDependencies` + `os` / `cpu` in the main package so npm
  resolves and installs only the matching one.
- Drop `install.js` and the network hop entirely.

The bin shim (`bin/tavora.js`) stays — it just resolves the platform
package via `require.resolve` instead of `path.join(__dirname, …)`.
