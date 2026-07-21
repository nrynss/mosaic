import { test, expect } from '@playwright/test';
import { playScenarioToRevision9, waitConnected } from './helpers';

test.describe('Decision history', () => {
  test('audit rows accumulate after handoffs', async ({ page }) => {
    await page.goto('/');
    await waitConnected(page);
    await playScenarioToRevision9(page);

    // Capture baseline from seeded fixture audits (if any).
    await page.getByTestId('tab-decision-history').click();
    await expect(page.getByTestId('decision-history')).toBeVisible();
    const beforeCount = await page.getByTestId('audit-record-row').count();

    // Back to workspace for handoffs.
    await page.getByTestId('tab-workspace').click();

    const dispatchNote = `History trail dispatch note ${Date.now()}`;
    const maintenanceNote = `History trail maintenance note ${Date.now()}`;

    await page.getByTestId('dispatch-note').fill(dispatchNote);
    await page.getByTestId('dispatch-save').click();
    await expect(page.getByTestId('dispatch-result')).toHaveAttribute('data-executed', 'false');

    await page.getByTestId('maintenance-note').fill(maintenanceNote);
    await page.getByTestId('maintenance-save').click();
    await expect(page.getByTestId('maintenance-result')).toHaveAttribute('data-executed', 'false');

    // Refresh advice so advisories (including audit_records) re-load.
    await page.getByTestId('refresh-advice').click();
    await expect(page.getByTestId('refresh-advice')).not.toHaveAttribute('data-state', 'loading');

    await page.getByTestId('tab-decision-history').click();
    await expect(page.getByTestId('decision-history')).toBeVisible();

    await expect
      .poll(async () => page.getByTestId('audit-record-row').count(), {
        timeout: 20_000,
        intervals: [100, 200, 500],
      })
      .toBeGreaterThanOrEqual(beforeCount + 2);

    // Prove the specific handoff notes landed (not just seeded sample audits).
    await expect(
      page.locator('[data-testid="audit-record-row"]').filter({ hasText: dispatchNote }),
    ).toHaveCount(1, { timeout: 10_000 });
    await expect(
      page.locator('[data-testid="audit-record-row"]').filter({ hasText: maintenanceNote }),
    ).toHaveCount(1);

    // Scenario beats from the play-through.
    await expect(page.getByTestId('scenario-beat-row').first()).toBeVisible();
    const beatCount = await page.getByTestId('scenario-beat-row').count();
    expect(beatCount).toBeGreaterThanOrEqual(10);
  });
});
