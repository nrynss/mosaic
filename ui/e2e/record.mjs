#!/usr/bin/env node
/**
 * Run the opt-in `record` Playwright project to produce the demo walkthrough
 * video (replay mode, slow beat spacing, 1920×1080, video always on).
 *
 * Cross-platform env injection (Windows-safe) — sets MOSAIC_E2E_RECORD=1, which
 * playwright.config.ts requires before the `record` project and its slow server
 * exist. Extra args pass through, e.g.:
 *
 *   node e2e/record.mjs --headed
 *   MOSAIC_E2E_HOLD_MS=4000 node e2e/record.mjs
 *   node e2e/record.mjs --live   # real OpenAI; loads OPENAI_API_KEY from
 *                               # process env or repo-root .env
 *
 * The video is written under test-results/ (gitignored). Path is printed at the
 * end. Post-process (speed up / trim) for the final cut as desired.
 */
import { spawn } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { ensureRootEnvKeys } from './load-root-env.mjs';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const uiRoot = path.resolve(__dirname, '..');
const resultsDir = path.join(uiRoot, 'test-results');
const recordingsDir = path.join(uiRoot, 'recordings');

// Find the most recent walkthrough video Playwright just wrote under
// test-results/. Playwright wipes test-results/ at the START of every run, so
// the artifact must be copied out here before any later run clobbers it.
function findLatestVideo() {
  if (!existsSync(resultsDir)) return null;
  let latest = null;
  for (const name of readdirSync(resultsDir)) {
    if (!name.startsWith('08-replay-walkthrough')) continue;
    const video = path.join(resultsDir, name, 'video.webm');
    if (!existsSync(video)) continue;
    const mtime = statSync(video).mtimeMs;
    if (!latest || mtime > latest.mtime) latest = { video, mtime };
  }
  return latest?.video ?? null;
}

// Archive the recording to a durable, un-wiped folder with a timestamped name
// so every take is kept on disk (ui/recordings/ is gitignored — large binaries).
function archiveVideo() {
  const src = findLatestVideo();
  if (!src) {
    console.warn('[record] no walkthrough video found to archive.');
    return null;
  }
  mkdirSync(recordingsDir, { recursive: true });
  const stamp = new Date()
    .toISOString()
    .replace(/[:.]/g, '-')
    .replace('T', '_')
    .slice(0, 19);
  const dest = path.join(recordingsDir, `demo-walkthrough-${recordMode}-${stamp}.webm`);
  copyFileSync(src, dest);
  return dest;
}

const rawArgs = process.argv.slice(2);
const live = rawArgs.includes('--live');
const passthrough = rawArgs.filter((a) => a !== '--live');
const recordMode = live ? 'live' : (process.env.MOSAIC_E2E_RECORD_MODE || 'replay');

if (live) {
  // Prefer shell env; if missing, pull OPENAI_API_KEY from repo-root .env
  // (gitignored) so `npm run test:e2e:record:live` works without a manual export.
  const { envPath, loaded, missing } = ensureRootEnvKeys(['OPENAI_API_KEY']);
  if (loaded.includes('OPENAI_API_KEY')) {
    console.log(`[record] loaded OPENAI_API_KEY from ${envPath}`);
  }
  if (missing.includes('OPENAI_API_KEY') || !String(process.env.OPENAI_API_KEY || '').trim()) {
    console.error(
      '[record] --live needs a funded OPENAI_API_KEY in the environment or in the repo-root .env.',
    );
    console.error(`[record] looked for: ${envPath}`);
    console.error('[record] Aborting before any spend.');
    process.exit(1);
  }
  console.warn(
    '[record] LIVE mode: this run makes REAL, billable OpenAI calls (per-beat Luna + operator Terra/Sol/Luna).',
  );
}

const env = {
  ...process.env,
  MOSAIC_E2E_RECORD: '1',
  MOSAIC_E2E_RECORD_MODE: recordMode,
};

const child = spawn(
  'npx',
  ['playwright', 'test', '--project=record', ...passthrough],
  {
    cwd: uiRoot,
    env,
    stdio: 'inherit',
    shell: process.platform === 'win32',
  },
);

child.on('exit', (code, signal) => {
  if (code === 0) {
    const archived = archiveVideo();
    if (archived) {
      console.log(`\n[record] Done. Recording archived → ${path.relative(uiRoot, archived)}`);
    } else {
      console.log('\n[record] Done, but no video was archived (check test-results/).');
    }
  }
  process.exit(signal ? 1 : (code ?? 0));
});

child.on('error', (err) => {
  console.error('[record] failed to launch playwright:', err);
  process.exit(1);
});
