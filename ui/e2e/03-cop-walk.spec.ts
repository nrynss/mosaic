import { test, expect } from '@playwright/test';
import { claimRow, playScenarioToRevision9, waitConnected } from './helpers';

test.describe('COP walk', () => {
  test('key entities and claim-class labels are present at rev 9', async ({ page }) => {
    await page.goto('/');
    await waitConnected(page);
    await playScenarioToRevision9(page);

    // Claim-class legend.
    await expect(page.getByTestId('claim-key-reported')).toBeVisible();
    await expect(page.getByTestId('claim-key-assessed')).toBeVisible();
    await expect(page.getByTestId('claim-key-recommended')).toBeVisible();

    const incident = claimRow(page, 'Incident', 'incident-domestic-001');
    await expect(incident).toBeVisible();
    await expect(incident.getByTestId('cop-claim-class')).toHaveAttribute(
      'data-claim-class',
      'reported_fact',
    );

    // Brook Lane ends open after correction (blocked → open story).
    const brook = claimRow(page, 'Road', 'road-brook-lane');
    await expect(brook).toBeVisible();
    await expect(brook.getByTestId('cop-claim-title')).toContainText(/open/i);
    await expect(brook.getByTestId('cop-claim-class')).toHaveAttribute(
      'data-claim-class',
      'reported_fact',
    );

    // Main street bridge remains blocked.
    const bridge = claimRow(page, 'Road', 'road-main-street-bridge');
    await expect(bridge).toBeVisible();
    await expect(bridge.getByTestId('cop-claim-title')).toContainText(/blocked/i);

    // EMS resource present (late beat leaves it unavailable).
    const ems = claimRow(page, 'Resource', 'resource-ems-004');
    await expect(ems).toBeVisible();
    await expect(ems.getByTestId('cop-claim-class')).toHaveAttribute(
      'data-claim-class',
      'reported_fact',
    );

    // Weather alert.
    const weather = claimRow(page, 'Weather', 'weather-heavy-rain-001');
    await expect(weather).toBeVisible();
    await expect(weather.getByTestId('cop-claim-class')).toHaveAttribute(
      'data-claim-class',
      'reported_fact',
    );

    // Unit assignment.
    const unit = claimRow(page, 'Unit', 'unit-017');
    await expect(unit).toBeVisible();
    await expect(unit.getByTestId('cop-claim-title')).toContainText(/assigned/i);
    await expect(unit.getByTestId('cop-claim-class')).toHaveAttribute(
      'data-claim-class',
      'reported_fact',
    );
  });
});
