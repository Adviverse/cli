#!/usr/bin/env node
// Thin launcher: downloads the matching Go binary from the GitHub release for this
// package's version on first run, caches it next to this file, then execs it.
'use strict';
const { spawnSync, execFileSync } = require('child_process');
const fs = require('fs');
const path = require('path');
const os = require('os');

const version = require('../package.json').version;
const goos = { darwin: 'darwin', linux: 'linux', win32: 'windows' }[process.platform];
const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
if (!goos || !arch) {
  console.error(`adviverse: unsupported platform ${process.platform}/${process.arch}`);
  process.exit(1);
}
const exe = path.join(__dirname, `.bin-${version}`, goos === 'windows' ? 'adviverse.exe' : 'adviverse');

async function install() {
  const ext = goos === 'windows' ? 'zip' : 'tar.gz';
  const url = `https://github.com/Adviverse/cli/releases/download/v${version}/adviverse_${goos}_${arch}.${ext}`;
  const res = await fetch(url);
  if (!res.ok) throw new Error(`download failed: HTTP ${res.status} ${url}`);
  const dir = path.dirname(exe);
  fs.mkdirSync(dir, { recursive: true });
  const archive = path.join(os.tmpdir(), `adviverse-${version}-${process.pid}.${ext}`);
  fs.writeFileSync(archive, Buffer.from(await res.arrayBuffer()));
  // bsdtar (macOS, Windows 10+) and GNU tar both extract these archives.
  execFileSync('tar', ['-xf', archive, '-C', dir], { stdio: 'inherit' });
  fs.unlinkSync(archive);
  if (goos !== 'windows') fs.chmodSync(exe, 0o755);
}

(async () => {
  if (!fs.existsSync(exe)) await install();
  const r = spawnSync(exe, process.argv.slice(2), { stdio: 'inherit' });
  process.exit(r.status ?? 1);
})().catch((e) => {
  console.error(`adviverse: ${e.message}`);
  process.exit(1);
});
