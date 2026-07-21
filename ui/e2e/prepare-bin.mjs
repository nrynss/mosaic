#!/usr/bin/env node
/**
 * Build mosaicdemo into ui/.e2e-bin/ for Playwright webServer.
 * Cross-platform (Windows → .exe).
 */
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync } from 'node:fs';
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
if (existsSync(outPath) && !force) {
  console.log(`e2e binary ready: ${outPath}`);
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
