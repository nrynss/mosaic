#!/usr/bin/env node
/**
 * Build mosaicdemo into ui/.e2e-bin/ for Playwright webServer.
 * Cross-platform (Windows → .exe).
 *
 * Rebuilds when:
 *   - --force / MOSAIC_E2E_REBUILD=1
 *   - binary missing
 *   - any watched Go source (cmd/mosaicdemo, internal, go.mod/sum) is newer than the binary
 *
 * Fast path: binary present and up-to-date → skip go build.
 */
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const uiRoot = path.resolve(__dirname, '..');
const repoRoot = path.resolve(uiRoot, '..');
const outDir = path.join(uiRoot, '.e2e-bin');
const binName = process.platform === 'win32' ? 'mosaicdemo.exe' : 'mosaicdemo';
const outPath = path.join(outDir, binName);

mkdirSync(outDir, { recursive: true });

const force = process.argv.includes('--force') || process.env.MOSAIC_E2E_REBUILD === '1';

/** @param {string} dir */
function collectFiles(dir, acc = []) {
  if (!existsSync(dir)) return acc;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name === 'testdata' || entry.name === 'vendor' || entry.name.startsWith('.')) {
        continue;
      }
      collectFiles(full, acc);
      continue;
    }
    if (entry.isFile() && (entry.name.endsWith('.go') || entry.name === 'go.mod' || entry.name === 'go.sum')) {
      acc.push(full);
    }
  }
  return acc;
}

function latestSourceMtimeMs() {
  const roots = [
    path.join(repoRoot, 'cmd', 'mosaicdemo'),
    path.join(repoRoot, 'internal'),
    path.join(repoRoot, 'go.mod'),
    path.join(repoRoot, 'go.sum'),
  ];
  let latest = 0;
  for (const root of roots) {
    if (!existsSync(root)) continue;
    const st = statSync(root);
    if (st.isFile()) {
      latest = Math.max(latest, st.mtimeMs);
      continue;
    }
    for (const file of collectFiles(root)) {
      try {
        latest = Math.max(latest, statSync(file).mtimeMs);
      } catch {
        // ignore transient races
      }
    }
  }
  return latest;
}

function needsRebuild() {
  if (force) return true;
  if (!existsSync(outPath)) return true;
  const binMtime = statSync(outPath).mtimeMs;
  const srcMtime = latestSourceMtimeMs();
  if (srcMtime > binMtime) {
    console.log('e2e binary stale (Go sources newer) — rebuilding');
    return true;
  }
  return false;
}

if (!needsRebuild()) {
  console.log(`e2e binary ready (up to date): ${outPath}`);
  process.exit(0);
}

console.log(`building mosaicdemo → ${outPath}`);
const result = spawnSync('go', ['build', '-o', outPath, './cmd/mosaicdemo'], {
  cwd: repoRoot,
  stdio: 'inherit',
  env: process.env,
  shell: process.platform === 'win32',
});

if (result.status !== 0) {
  console.error('go build failed');
  process.exit(result.status ?? 1);
}

if (!existsSync(outPath)) {
  console.error(`binary missing after build: ${outPath}`);
  process.exit(1);
}

console.log('build ok');
process.exit(0);
