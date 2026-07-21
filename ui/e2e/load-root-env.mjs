/**
 * Load selected keys from the repository-root `.env` into `process.env`.
 *
 * Used by opt-in live e2e paths so a funded OPENAI_API_KEY in the gitignored
 * root `.env` is enough — no manual export required. Existing process env
 * always wins (never overwrite). Values are never logged.
 */
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
/** ui/e2e → ui → repo root */
export const repoRoot = path.resolve(__dirname, '..', '..');
export const rootEnvPath = path.join(repoRoot, '.env');

/**
 * Parse a dotenv-style file into a flat string map.
 * Supports KEY=VALUE, optional single/double quotes, # comments, blank lines.
 * Does not expand variables or multiline values.
 *
 * @param {string} text
 * @returns {Record<string, string>}
 */
export function parseDotEnv(text) {
  /** @type {Record<string, string>} */
  const out = {};
  for (const raw of String(text).split(/\r?\n/)) {
    const line = raw.trim();
    if (!line || line.startsWith('#')) continue;
    const eq = line.indexOf('=');
    if (eq <= 0) continue;
    const key = line.slice(0, eq).trim();
    if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) continue;
    let value = line.slice(eq + 1).trim();
    if (
      (value.startsWith('"') && value.endsWith('"')) ||
      (value.startsWith("'") && value.endsWith("'"))
    ) {
      value = value.slice(1, -1);
    }
    out[key] = value;
  }
  return out;
}

/**
 * Ensure each key is present on process.env, loading from repo-root `.env`
 * when missing (or when `force` is true).
 *
 * @param {string[]} keys
 * @param {{ force?: boolean, envPath?: string }} [opts]
 * @returns {{ envPath: string, loaded: string[], alreadySet: string[], missing: string[] }}
 */
export function ensureRootEnvKeys(keys, opts = {}) {
  const envPath = opts.envPath || rootEnvPath;
  const force = Boolean(opts.force);
  /** @type {string[]} */
  const loaded = [];
  /** @type {string[]} */
  const alreadySet = [];
  /** @type {string[]} */
  const missing = [];

  /** @type {Record<string, string> | null} */
  let fileMap = null;
  if (existsSync(envPath)) {
    try {
      fileMap = parseDotEnv(readFileSync(envPath, 'utf8'));
    } catch {
      fileMap = null;
    }
  }

  for (const key of keys) {
    const current = process.env[key];
    if (!force && current != null && String(current).trim() !== '') {
      alreadySet.push(key);
      continue;
    }
    const fromFile = fileMap?.[key];
    if (fromFile != null && String(fromFile).trim() !== '') {
      process.env[key] = String(fromFile);
      loaded.push(key);
      continue;
    }
    missing.push(key);
  }

  return { envPath, loaded, alreadySet, missing };
}
