import { test, expect } from '@playwright/test';
import { playScenarioToRevision9, waitConnected } from './helpers';

test.describe('Handoffs', () => {
  test('dispatch and maintenance notes save with executed:false', async ({ page }) => {
    await page.goto('/');
    await waitConnected(page);
    await playScenarioToRevision9(page);

    await page.getByTestId('dispatch-note').fill('E2E dispatch note — synthetic only.');
    await page.getByTestId('dispatch-save').click();
    await expect(page.getByTestId('dispatch-result')).toBeVisible();
    await expect(page.getByTestId('dispatch-result')).toHaveAttribute('data-executed', 'false');
    await expect(page.getByTestId('dispatch-result')).toHaveAttribute('data-delivered', 'false');
    await expect(page.getByTestId('dispatch-executed')).toHaveText('executed:false');

    await page.getByTestId('maintenance-note').fill('E2E maintenance note — Brook Lane synthetic.');
    await page.getByTestId('maintenance-save').click();
    await expect(page.getByTestId('maintenance-result')).toBeVisible();
    await expect(page.getByTestId('maintenance-result')).toHaveAttribute('data-executed', 'false');
    await expect(page.getByTestId('maintenance-result')).toHaveAttribute('data-delivered', 'false');
    await expect(page.getByTestId('maintenance-executed')).toHaveText('executed:false');
  });
});
