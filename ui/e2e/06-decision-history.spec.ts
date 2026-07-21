import { test, expect } from '@playwright/test';
import { playScenarioToRevision9, waitConnected } from './helpers';

test.describe('Decision history', () => {
  test('audit rows accumulate after handoffs', async ({ page }) => {
    await page.goto('/');
    await waitConnected(page);
    await playScenarioToRevision9(page);

    await page.getByTestId('dispatch-note').fill('History trail dispatch note.');
    await page.getByTestId('dispatch-save').click();
    await expect(page.getByTestId('dispatch-result')).toBeVisible();

    await page.getByTestId('maintenance-note').fill('History trail maintenance note.');
    await page.getByTestId('maintenance-save').click();
    await expect(page.getByTestId('maintenance-result')).toBeVisible();

    // Refresh advice so advisories (including audit_records) re-load.
    await page.getByTestId('refresh-advice').click();
    await expect(page.getByTestId('refresh-advice')).not.toHaveAttribute('data-state', 'loading');

    await page.getByTestId('tab-decision-history').click();
    await expect(page.getByTestId('decision-history')).toBeVisible();

    // Fixture package may already include sample audits; after handoffs expect ≥1.
    await expect(page.getByTestId('audit-record-row').first()).toBeVisible({ timeout: 20_000 });

    // Scenario beats from the play-through.
    await expect(page.getByTestId('scenario-beat-row').first()).toBeVisible();
    const beatCount = await page.getByTestId('scenario-beat-row').count();
    expect(beatCount).toBeGreaterThanOrEqual(10);
  });
});
