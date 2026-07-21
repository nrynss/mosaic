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
 * OPENAI_API_KEY is cleared; provider flags forced to fixture unless
 * MOSAIC_E2E_ALLOW_AMBIENT_PROVIDERS=1.
 */
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { existsSync, mkdirSync, readdirSync, unlinkSync, statSync } from 'node:fs';
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
const dbDir = path.join(os.tmpdir(), 'mosaic-playwright-e2e');
mkdirSync(dbDir, { recursive: true });
const dbPath = path.join(
  dbDir,
  `demo-${mode}-${port}-${process.pid}-${Date.now()}.db`,
);

/** Fail fast with a clear message when the port is already taken. */
async function assertPortFree(portNum) {
  await new Promise((resolve, reject) => {
    const server = createServer();
    server.unref();
    server.once('error', (err) => {
      if (err && err.code === 'EADDRINUSE') {
        reject(
          new Error(
            `port ${portNum} is already in use. Stop leftover mosaicdemo, or set MOSAIC_E2E_FIXTURE_PORT / MOSAIC_E2E_REPLAY_PORT to free ports.`,
          ),
        );
        return;
      }
      reject(err);
    });
    server.listen(Number(portNum), '127.0.0.1', () => {
      server.close((closeErr) => (closeErr ? reject(closeErr) : resolve()));
    });
  });
}

/** Best-effort cleanup of old e2e SQLite files (>6h). */
function cleanupStaleTempDbs() {
  try {
    const cutoff = Date.now() - 6 * 60 * 60 * 1000;
    for (const name of readdirSync(dbDir)) {
      if (!name.startsWith('demo-') || !name.endsWith('.db')) continue;
      const full = path.join(dbDir, name);
      try {
        if (statSync(full).mtimeMs < cutoff) unlinkSync(full);
      } catch {
        // ignore
      }
    }
  } catch {
    // ignore
  }
}

function unlinkDb() {
  for (const suffix of ['', '-wal', '-shm']) {
    const p = dbPath + suffix;
    try {
      if (existsSync(p)) unlinkSync(p);
    } catch {
      // ignore
    }
  }
}

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

cleanupStaleTempDbs();

try {
  await assertPortFree(port);
} catch (err) {
  console.error(`[start-demo] ${err.message || err}`);
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

// Always force fixture providers unless explicitly opting into ambient shell values.
// Key is still cleared, so even live cannot spend — but badges would otherwise drift.
const allowAmbient = process.env.MOSAIC_E2E_ALLOW_AMBIENT_PROVIDERS === '1';
if (!allowAmbient) {
  env.MOSAIC_LUNA_PROVIDER = 'fixture';
  env.MOSAIC_TERRA_PROVIDER = 'fixture';
  env.MOSAIC_SOL_PROVIDER = 'fixture';
} else {
  env.MOSAIC_LUNA_PROVIDER = env.MOSAIC_LUNA_PROVIDER || 'fixture';
  env.MOSAIC_TERRA_PROVIDER = env.MOSAIC_TERRA_PROVIDER || 'fixture';
  env.MOSAIC_SOL_PROVIDER = env.MOSAIC_SOL_PROVIDER || 'fixture';
}

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
    try {
      child.kill(signal);
    } catch {
      // ignore
    }
  }
};

process.on('SIGINT', () => {
  shutdown('SIGINT');
  unlinkDb();
});
process.on('SIGTERM', () => {
  shutdown('SIGTERM');
  unlinkDb();
});
process.on('exit', () => {
  shutdown('SIGTERM');
  unlinkDb();
});

child.on('exit', (code, signal) => {
  unlinkDb();
  if (signal) {
    process.exit(1);
  }
  process.exit(code ?? 0);
});

child.on('error', (err) => {
  console.error('[start-demo] failed to spawn:', err);
  unlinkDb();
  process.exit(1);
});
