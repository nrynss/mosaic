import { test, expect } from '@playwright/test';
import {
  claimRow,
  insightCard,
  playScenarioToRevision9,
  waitConnected,
} from './helpers';

/**
 * Full demo narrative as a single recording-friendly walkthrough.
 * Walkthrough project enables video+trace always (see playwright.config.ts).
 */
test.describe('Demo walkthrough', () => {
  test('connection → play → COP → advice → handoffs → history', async ({ page }) => {
    await page.goto('/');
    await waitConnected(page);
    await expect(page.getByTestId('ai-mode-pill')).toHaveAttribute('data-state', 'idle');

    await playScenarioToRevision9(page);
    await expect(claimRow(page, 'Incident', 'incident-domestic-001')).toBeVisible();
    await expect(claimRow(page, 'Road', 'road-brook-lane').getByTestId('cop-claim-title')).toContainText(
      /open/i,
    );

    await page.getByTestId('refresh-advice').click();
    await expect(insightCard(page, 'insight-domestic-access-001')).toBeVisible();
    await expect(insightCard(page, 'insight-domestic-access-001')).toHaveAttribute(
      'data-status',
      /superseded|not_current|obsolete/,
    );

    await page.getByTestId('dispatch-note').fill('Walkthrough: unit on scene (synthetic).');
    await page.getByTestId('dispatch-save').click();
    await expect(page.getByTestId('dispatch-result')).toHaveAttribute('data-executed', 'false');

    await page.getByTestId('maintenance-note').fill('Walkthrough: Brook Lane note (synthetic).');
    await page.getByTestId('maintenance-save').click();
    await expect(page.getByTestId('maintenance-result')).toHaveAttribute('data-executed', 'false');

    await page.getByTestId('refresh-advice').click();
    await page.getByTestId('tab-decision-history').click();
    await expect(page.getByTestId('decision-history')).toBeVisible();
    await expect(page.getByTestId('audit-record-row').first()).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId('scenario-beat-row').first()).toBeVisible();
  });
});
