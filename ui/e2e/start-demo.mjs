#!/usr/bin/env node
/**
 * Launch mosaicdemo for Playwright webServer (fixture or replay).
 *
 * Usage:
 *   node e2e/start-demo.mjs --mode=fixture --port=18080
 *   node e2e/start-demo.mjs --mode=replay --port=18081
 *
 * Expects:
 *   - ui/dist built (npm run build)
 *   - ui/.e2e-bin/mosaicdemo[.exe] (npm run e2e:prepare)
 *
 * Paths are always absolute native paths (Windows-safe).
 * OPENAI_API_KEY is cleared so tests never hit live OpenAI.
 */
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const uiRoot = path.resolve(__dirname, '..');
const repoRoot = path.resolve(uiRoot, '..');

function argValue(name, fallback) {
  const prefix = `--${name}=`;
  const hit = process.argv.find((a) => a.startsWith(prefix));
  if (hit) return hit.slice(prefix.length);
  const idx = process.argv.indexOf(`--${name}`);
  if (idx >= 0 && process.argv[idx + 1]) return process.argv[idx + 1];
  return fallback;
}

const mode = String(argValue('mode', process.env.MOSAIC_E2E_MODE || 'fixture')).toLowerCase();
const port = String(argValue('port', process.env.MOSAIC_E2E_PORT || '18080'));
const listen = `127.0.0.1:${port}`;

if (mode !== 'fixture' && mode !== 'replay') {
  console.error(`unknown mode ${mode}; use fixture|replay`);
  process.exit(2);
}

const binName = process.platform === 'win32' ? 'mosaicdemo.exe' : 'mosaicdemo';
const defaultBin = path.join(uiRoot, '.e2e-bin', binName);
const bin = process.env.MOSAIC_E2E_BIN
  ? path.resolve(process.env.MOSAIC_E2E_BIN)
  : defaultBin;

const uiDir = path.join(uiRoot, 'dist');
const cassetteDir = path.join(repoRoot, 'testdata', 'demo', 'cassettes');
// Unique DB per process so prior Terra/Sol insights cannot collide
// ("Insight already exists") across Playwright runs on a fixed port.
const dbDir = path.join(os.tmpdir(), 'mosaic-playwright-e2e');
mkdirSync(dbDir, { recursive: true });
const dbPath = path.join(
  dbDir,
  `demo-${mode}-${port}-${process.pid}-${Date.now()}.db`,
);

if (!existsSync(bin)) {
  console.error(`mosaicdemo binary not found: ${bin}`);
  console.error('Run: npm run e2e:prepare  (from ui/)');
  process.exit(1);
}
if (!existsSync(uiDir)) {
  console.error(`ui dist missing: ${uiDir}`);
  console.error('Run: npm run build  (from ui/)');
  process.exit(1);
}
if (mode === 'replay' && !existsSync(cassetteDir)) {
  console.error(`cassette bank missing: ${cassetteDir}`);
  process.exit(1);
}

// Strip ambient OpenAI key so fixture/replay cannot call live APIs.
const env = { ...process.env };
delete env.OPENAI_API_KEY;
env.OPENAI_API_KEY = '';
env.MOSAIC_SEED_ON_START = '0';
env.MOSAIC_SIM_BEAT_SPACING = '1ms';
env.MOSAIC_SIM_MODE = mode === 'replay' ? 'replay' : 'fixture';
env.MOSAIC_UI_DIR = uiDir;
if (mode === 'replay') {
  env.MOSAIC_CASSETTE_DIR = cassetteDir;
}
// Keep provider flags off live for the e2e binary.
env.MOSAIC_LUNA_PROVIDER = env.MOSAIC_LUNA_PROVIDER || 'fixture';
env.MOSAIC_TERRA_PROVIDER = env.MOSAIC_TERRA_PROVIDER || 'fixture';
env.MOSAIC_SOL_PROVIDER = env.MOSAIC_SOL_PROVIDER || 'fixture';

const args = [
  '-listen-addr',
  listen,
  '-db',
  dbPath,
  '-ui-dir',
  uiDir,
  '-asset-root',
  repoRoot,
];

console.log(`[start-demo] mode=${mode} listen=${listen}`);
console.log(`[start-demo] bin=${bin}`);
console.log(`[start-demo] ui=${uiDir}`);
console.log(`[start-demo] db=${dbPath}`);
if (mode === 'replay') {
  console.log(`[start-demo] cassettes=${cassetteDir}`);
}

const child = spawn(bin, args, {
  cwd: repoRoot,
  env,
  stdio: 'inherit',
  windowsHide: true,
});

const shutdown = (signal) => {
  if (!child.killed) {
    child.kill(signal);
  }
};

process.on('SIGINT', () => shutdown('SIGINT'));
process.on('SIGTERM', () => shutdown('SIGTERM'));
// Playwright on Windows often sends the parent a kill; forward if possible.
process.on('exit', () => shutdown('SIGTERM'));

child.on('exit', (code, signal) => {
  if (signal) {
    process.exit(1);
  }
  process.exit(code ?? 0);
});

child.on('error', (err) => {
  console.error('[start-demo] failed to spawn:', err);
  process.exit(1);
});
