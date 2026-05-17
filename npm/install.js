#!/usr/bin/env node
// install.js — runs on `npm install @tavora/cli`. Downloads the
// prebuilt Tavora binary for the current platform from a GitHub
// Release and unpacks it into ./bin/. The JS shim in bin/tavora.js
// execs it.
//
// Pattern matches esbuild / prisma / wrangler: ship a tiny npm
// package, do the platform-specific binary pull at install time.
// Single-package variant (not optional-deps); migration to per-
// platform optional-deps is a follow-up when install reliability or
// offline-install matters enough.

'use strict';

const fs = require('node:fs');
const path = require('node:path');
const https = require('node:https');
const zlib = require('node:zlib');
const { pipeline } = require('node:stream/promises');

const pkg = require('./package.json');
const VERSION = pkg.version;

// Skip the download in CI sandboxes / monorepo installs where the
// network isn't reachable. The user's first invocation of `tavora`
// from the shim will then print a clear error pointing them at the
// re-install path.
if (process.env.TAVORA_SKIP_DOWNLOAD === '1') {
  console.log('tavora: TAVORA_SKIP_DOWNLOAD=1 — skipping binary download');
  process.exit(0);
}

// Map node platform/arch to the release artifact name. The Go
// release pipeline (see ../README.md §Releasing) produces gzipped
// binaries with these names. Windows still gets a gzipped .exe
// rather than a .zip — keeps the install path identical.
const ARTIFACTS = {
  'darwin-arm64': { archive: 'tavora-darwin-arm64.gz', exe: 'tavora' },
  'darwin-x64':   { archive: 'tavora-darwin-amd64.gz', exe: 'tavora' },
  'linux-arm64':  { archive: 'tavora-linux-arm64.gz',  exe: 'tavora' },
  'linux-x64':    { archive: 'tavora-linux-amd64.gz',  exe: 'tavora' },
  'win32-x64':    { archive: 'tavora-windows-amd64.exe.gz', exe: 'tavora.exe' },
};

const key = `${process.platform}-${process.arch}`;
const artifact = ARTIFACTS[key];
if (!artifact) {
  console.error(`tavora: no prebuilt binary for ${key}.`);
  console.error('Build from source: https://github.com/tavora-ai/tavora-cli');
  process.exit(1);
}

const releaseURL = `https://github.com/tavora-ai/tavora-cli/releases/download/v${VERSION}/${artifact.archive}`;
const binDir = path.join(__dirname, 'bin');
const binPath = path.join(binDir, artifact.exe);

(async () => {
  fs.mkdirSync(binDir, { recursive: true });

  console.log(`tavora: fetching ${artifact.archive} (v${VERSION})…`);
  const res = await fetchWithRedirects(releaseURL);
  if (res.statusCode !== 200) {
    console.error(`tavora: download failed (HTTP ${res.statusCode}) — ${releaseURL}`);
    console.error('If you are installing a pre-release version, the asset may not exist yet.');
    console.error('Build from source instead: https://github.com/tavora-ai/tavora-cli');
    process.exit(1);
  }

  await pipeline(res, zlib.createGunzip(), fs.createWriteStream(binPath));
  if (process.platform !== 'win32') {
    fs.chmodSync(binPath, 0o755);
  }
  console.log(`tavora: installed at ${binPath}`);
})().catch((err) => {
  console.error('tavora: install failed:', err.message);
  process.exit(1);
});

// Follow HTTP 30x redirects up to 5 hops. GitHub Releases serve a
// 302 redirect to a CDN URL on every download — naively GET-ing
// the release URL returns the redirect page instead of the binary.
function fetchWithRedirects(url, hop = 0) {
  return new Promise((resolve, reject) => {
    if (hop > 5) return reject(new Error('too many redirects'));
    https.get(url, (res) => {
      if ([301, 302, 303, 307, 308].includes(res.statusCode) && res.headers.location) {
        res.resume();
        resolve(fetchWithRedirects(res.headers.location, hop + 1));
        return;
      }
      resolve(res);
    }).on('error', reject);
  });
}
