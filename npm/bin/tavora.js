#!/usr/bin/env node
// bin/tavora.js — the entry point npm registers for the `tavora`
// command. Locates the platform-specific binary that install.js
// downloaded alongside this script and execs it with the user's
// argv. Designed to be near-zero-overhead: no parsing, no
// transformation — the Go binary handles everything.

'use strict';

const { spawn } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

const exeName = process.platform === 'win32' ? 'tavora.exe' : 'tavora';
const binPath = path.join(__dirname, exeName);

if (!fs.existsSync(binPath)) {
  console.error(`tavora: binary not found at ${binPath}.`);
  console.error('The postinstall download probably failed. Try reinstalling:');
  console.error('  npm i -g @tavora/cli --force');
  console.error('Or skip the download and use a local binary:');
  console.error('  TAVORA_SKIP_DOWNLOAD=1 npm i -g @tavora/cli');
  process.exit(127);
}

const child = spawn(binPath, process.argv.slice(2), {
  stdio: 'inherit',
  windowsHide: false,
});

// Forward POSIX signals so Ctrl-C in `tavora dev` reaches the Go
// watcher cleanly. Without this, npm's wrapper script may swallow
// SIGINT and leave the binary running.
for (const sig of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
  process.on(sig, () => {
    if (!child.killed) child.kill(sig);
  });
}

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 0);
});
child.on('error', (err) => {
  console.error('tavora: failed to launch binary:', err.message);
  process.exit(126);
});
