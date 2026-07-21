import { test, expect } from '@playwright/test';
import {
  claimRow,
  insightCard,
  playScenarioToRevision9,
  recommendationCard,
  waitConnected,
} from './helpers';

test.describe('Refresh advice + supersession', () => {
  test('refresh re-polls advisories without mutating COP; history shows rev-7 card', async ({
    page,
  }) => {
    await page.goto('/');
    await waitConnected(page);
    await playScenarioToRevision9(page);

    await expect(page.getByTestId('cop-revision')).toHaveAttribute('data-revision', '9');
    const claimCountBefore = await page.getByTestId('cop-claim-row').count();
    expect(claimCountBefore).toBeGreaterThan(0);
    const brookTitle = await claimRow(page, 'Road', 'road-brook-lane')
      .getByTestId('cop-claim-title')
      .textContent();

    await page.getByTestId('refresh-advice').click();
    await expect(page.getByTestId('refresh-advice')).not.toHaveAttribute('data-state', 'loading', {
      timeout: 15_000,
    });

    // Board unchanged (advice never mutates COP).
    await expect(page.getByTestId('cop-revision')).toHaveAttribute('data-revision', '9');
    await expect(page.getByTestId('cop-claim-row')).toHaveCount(claimCountBefore);
    await expect(
      claimRow(page, 'Road', 'road-brook-lane').getByTestId('cop-claim-title'),
    ).toHaveText(brookTitle || '');

    // Access insight present and superseded (or obsolete mapped to superseded).
    const access = insightCard(page, 'insight-domestic-access-001');
    await expect(access).toBeVisible();
    const accessStatus = await access.getAttribute('data-status');
    expect(['superseded', 'not_current', 'obsolete']).toContain(accessStatus);

    // Later obsolete insight also present under history (default showHistory=true).
    const obsolete = insightCard(page, 'insight-domestic-access-001-obsolete');
    await expect(obsolete).toBeVisible();

    // Recommendation not current at rev 9.
    const rec = recommendationCard(page, 'recommendation-domestic-001');
    await expect(rec).toBeVisible();
    await expect(rec).toHaveAttribute('data-status', 'not_current');

    // Toggle history hides past cards.
    await page.getByTestId('advice-history-toggle').click();
    await expect(page.getByTestId('advice-history-toggle')).toHaveAttribute(
      'data-show-history',
      'false',
    );
    // Superseded cards filter out when history hidden and no current.
    await expect(access).toHaveCount(0);

    // Restore history — rev-7 access card still queryable at rev 9.
    await page.getByTestId('advice-history-toggle').click();
    await expect(page.getByTestId('advice-history-toggle')).toHaveAttribute(
      'data-show-history',
      'true',
    );
    await expect(insightCard(page, 'insight-domestic-access-001')).toBeVisible();
  });
});
