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
 * fixture/replay: OPENAI_API_KEY is cleared; provider flags forced to fixture
 * unless MOSAIC_E2E_ALLOW_AMBIENT_PROVIDERS=1.
 * live: keeps/loads OPENAI_API_KEY (shell env, else repo-root .env) and forces
 * live providers + MOSAIC_SIM_MODE=record; banks under ui/recordings/cassettes-live.
 */
import { spawn } from 'node:child_process';
import { createServer } from 'node:net';
import { existsSync, mkdirSync, readdirSync, unlinkSync, statSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { ensureRootEnvKeys } from './load-root-env.mjs';

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
// Beat spacing is 1ms for fast, deterministic regression runs. The recording
// server overrides it (e.g. --beat-spacing=2600ms) so the progressive COP
// reveal is slow enough to narrate. Go parses this as time.Duration.
const beatSpacing = String(
  argValue('beat-spacing', process.env.MOSAIC_E2E_BEAT_SPACING || '1ms'),
);

if (mode !== 'fixture' && mode !== 'replay' && mode !== 'live') {
  console.error(`unknown mode ${mode}; use fixture|replay|live`);
  process.exit(2);
}

const binName = process.platform === 'win32' ? 'mosaicdemo.exe' : 'mosaicdemo';
const defaultBin = path.join(uiRoot, '.e2e-bin', binName);
const bin = process.env.MOSAIC_E2E_BIN
  ? path.resolve(process.env.MOSAIC_E2E_BIN)
  : defaultBin;

const uiDir = path.join(uiRoot, 'dist');
const cassetteDir = path.join(repoRoot, 'testdata', 'demo', 'cassettes');
// Live mode banks fresh cassettes HERE, never over the committed replay bank
// (replay tests depend on that bank being byte-stable). Gitignored under ui/.
const liveCassetteDir = path.join(uiRoot, 'recordings', 'cassettes-live');
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

const env = { ...process.env };
env.MOSAIC_SEED_ON_START = '0';
env.MOSAIC_SIM_BEAT_SPACING = beatSpacing;
env.MOSAIC_UI_DIR = uiDir;

if (mode === 'live') {
  // LIVE recording: REAL OpenAI calls ($$$). Requires a funded key. SIM_MODE
  // record so the run also banks a cassette — to liveCassetteDir, never the
  // committed replay bank. Providers forced live.
  // If the parent (record.mjs / shell) did not export the key, load it from
  // the repo-root .env so live mode still works.
  const { envPath, loaded } = ensureRootEnvKeys(['OPENAI_API_KEY']);
  if (loaded.includes('OPENAI_API_KEY')) {
    env.OPENAI_API_KEY = process.env.OPENAI_API_KEY;
    console.log(`[start-demo] loaded OPENAI_API_KEY from ${envPath}`);
  } else if (process.env.OPENAI_API_KEY) {
    env.OPENAI_API_KEY = process.env.OPENAI_API_KEY;
  }
  if (!String(env.OPENAI_API_KEY || '').trim()) {
    console.error(
      '[start-demo] live mode requires a funded OPENAI_API_KEY in the environment or repo-root .env.',
    );
    console.error(`[start-demo] looked for: ${envPath}`);
    process.exit(1);
  }
  mkdirSync(liveCassetteDir, { recursive: true });
  env.MOSAIC_SIM_MODE = 'record';
  env.MOSAIC_CASSETTE_DIR = liveCassetteDir;
  env.MOSAIC_LUNA_PROVIDER = 'live';
  env.MOSAIC_TERRA_PROVIDER = 'live';
  env.MOSAIC_SOL_PROVIDER = 'live';
} else {
  // fixture / replay: strip the key so no live call is possible, force fixture
  // providers (badges would otherwise drift), and point replay at the bank.
  delete env.OPENAI_API_KEY;
  env.OPENAI_API_KEY = '';
  env.MOSAIC_SIM_MODE = mode === 'replay' ? 'replay' : 'fixture';
  if (mode === 'replay') {
    env.MOSAIC_CASSETTE_DIR = cassetteDir;
  }
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

console.log(`[start-demo] mode=${mode} listen=${listen} beat-spacing=${beatSpacing}`);
console.log(`[start-demo] bin=${bin}`);
console.log(`[start-demo] ui=${uiDir}`);
console.log(`[start-demo] db=${dbPath}`);
if (mode === 'replay') {
  console.log(`[start-demo] cassettes(read)=${cassetteDir}`);
} else if (mode === 'live') {
  console.log(`[start-demo] LIVE — real OpenAI calls; banking to ${liveCassetteDir}`);
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
