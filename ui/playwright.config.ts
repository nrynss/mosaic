import { defineConfig, devices } from '@playwright/test';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const FIXTURE_PORT = process.env.MOSAIC_E2E_FIXTURE_PORT || '18080';
const REPLAY_PORT = process.env.MOSAIC_E2E_REPLAY_PORT || '18081';
const FIXTURE_URL = `http://127.0.0.1:${FIXTURE_PORT}`;
const REPLAY_URL = `http://127.0.0.1:${REPLAY_PORT}`;

/**
 * Playwright config for the demo UI against built mosaicdemo (ui/dist).
 * Primary project: fixture mode. Secondary: replay + model actions (cassette bank).
 */
export default defineConfig({
  testDir: path.join(__dirname, 'e2e'),
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: process.env.CI
    ? [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]]
    : [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
  timeout: 90_000,
  expect: {
    timeout: 20_000,
  },
  use: {
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    viewport: { width: 1440, height: 900 },
    colorScheme: 'dark',
    reducedMotion: 'reduce',
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
  },
  projects: [
    {
      name: 'fixture',
      testMatch: /0[1-6]-.*\.spec\.ts$/,
      use: {
        ...devices['Desktop Chrome'],
        baseURL: FIXTURE_URL,
        viewport: { width: 1440, height: 900 },
        colorScheme: 'dark',
        reducedMotion: 'reduce',
      },
    },
    {
      name: 'walkthrough',
      testMatch: /07-demo-walkthrough\.spec\.ts$/,
      use: {
        ...devices['Desktop Chrome'],
        baseURL: FIXTURE_URL,
        viewport: { width: 1440, height: 900 },
        colorScheme: 'dark',
        reducedMotion: 'reduce',
        video: 'on',
        trace: 'on',
      },
    },
    {
      name: 'replay',
      testMatch: /replay-.*\.spec\.ts$/,
      use: {
        ...devices['Desktop Chrome'],
        baseURL: REPLAY_URL,
        viewport: { width: 1440, height: 900 },
        colorScheme: 'dark',
        reducedMotion: 'reduce',
      },
    },
  ],
  // Both servers always start so mixed project filters still work without
  // reconfiguring. Fixture-only still boots replay (needs free REPLAY_PORT +
  // cassette bank). Override ports via MOSAIC_E2E_FIXTURE_PORT / MOSAIC_E2E_REPLAY_PORT.
  webServer: [
    {
      command: `node e2e/start-demo.mjs --mode=fixture --port=${FIXTURE_PORT}`,
      url: `${FIXTURE_URL}/api/v1/health`,
      // Prefer a clean process each run so prior COP/session state cannot leak.
      reuseExistingServer: process.env.MOSAIC_E2E_REUSE === '1',
      timeout: 120_000,
      cwd: __dirname,
    },
    {
      command: `node e2e/start-demo.mjs --mode=replay --port=${REPLAY_PORT}`,
      url: `${REPLAY_URL}/api/v1/health`,
      reuseExistingServer: process.env.MOSAIC_E2E_REUSE === '1',
      timeout: 120_000,
      cwd: __dirname,
    },
  ],
});
