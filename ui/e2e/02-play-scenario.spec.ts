import { test, expect } from '@playwright/test';
import { playScenarioToRevision9, waitConnected } from './helpers';

test.describe('Play scenario', () => {
  test('play reaches COP revision 9 with populated sections', async ({ page }) => {
    await page.goto('/');
    await waitConnected(page);
    await playScenarioToRevision9(page);

    await expect(page.getByTestId('run-status')).toHaveAttribute('data-status', 'ended');
    await expect(page.getByTestId('cop-revision')).toHaveAttribute('data-revision', '9');

    // All COP entity kinds present after 10 beats.
    for (const kind of ['Incident', 'Unit', 'Resource', 'Road', 'Weather'] as const) {
      await expect(page.locator(`[data-testid="cop-claim-row"][data-kind="${kind}"]`).first()).toBeVisible();
    }

    // Status board counts non-zero.
    await expect(page.getByTestId('rail-units-count')).not.toHaveText('0');
    await expect(page.getByTestId('rail-roads-count')).not.toHaveText('0');
    await expect(page.getByTestId('rail-resources-count')).not.toHaveText('0');
    await expect(page.getByTestId('rail-weather-count')).not.toHaveText('0');
  });
});
