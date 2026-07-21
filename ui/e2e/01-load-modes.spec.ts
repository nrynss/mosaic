import { test, expect } from '@playwright/test';
import { waitConnected } from './helpers';

test.describe('Load + modes', () => {
  test('app loads with connected stream and fixture mode badges', async ({ page }) => {
    await page.goto('/');
    await waitConnected(page);

    // No live OpenAI key under fixture e2e.
    await expect(page.getByTestId('ai-mode-pill')).toHaveAttribute('data-state', 'idle');

    await expect(page.getByTestId('play-scenario')).toBeVisible();
    await expect(page.getByTestId('start-over')).toBeVisible();
    await expect(page.getByTestId('run-status')).toBeVisible();

    // Mode badges default to fixture / demo pack (data-mode).
    for (const agent of ['luna', 'terra', 'sol'] as const) {
      const badge = page.getByTestId(`mode-badge-${agent}`);
      await expect(badge).toBeVisible();
      const mode = await badge.getAttribute('data-mode');
      expect(mode === 'fixture' || mode === 'mosaic-fixture' || mode === null || mode === '').toBeTruthy();
    }

    // COP empty or zero before Play (seed off).
    const rev = page.getByTestId('cop-revision');
    await expect(rev).toBeVisible();
    const revision = await rev.getAttribute('data-revision');
    expect(revision === '' || revision === '0' || revision === '—').toBeTruthy();
  });
});
