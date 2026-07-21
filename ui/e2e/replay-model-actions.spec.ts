import { test, expect } from '@playwright/test';
import {
  expectBankedModelResult,
  playScenarioToRevision9,
  waitConnected,
  waitOperatorPOST,
} from './helpers';

/**
 * Replay project: drive model UI affordances against testdata/demo/cassettes.
 * No OPENAI_API_KEY — bank hits only.
 */
test.describe('Replay model actions', () => {
  test('Terra / Sol / Luna hit bank after Play to COP rev 9', async ({ page }) => {
    await page.goto('/');
    await waitConnected(page);

    // Interactions load; cassette mode should report replay.
    await expect(page.getByTestId('model-actions')).toBeVisible();
    await expect(page.getByTestId('model-cassette-mode')).toHaveText(/replay/i, {
      timeout: 20_000,
    });

    // Terra/Sol gated until COP rev 9.
    await expect(page.getByTestId('generate-assessment')).toBeDisabled();
    await expect(page.getByTestId('request-briefing')).toBeDisabled();

    await playScenarioToRevision9(page);

    await expect(page.getByTestId('model-cop-gate')).toHaveAttribute('data-ready', 'true');
    await expect(page.getByTestId('generate-assessment')).toBeEnabled();
    await expect(page.getByTestId('request-briefing')).toBeEnabled();

    const revBefore = await page.getByTestId('cop-revision').getAttribute('data-revision');
    const claimCountBefore = await page.getByTestId('cop-claim-row').count();
    expect(claimCountBefore).toBeGreaterThan(0);

    // Terra assessment from bank.
    const terraRespPromise = waitOperatorPOST(page, 'analyze');
    await page.getByTestId('generate-assessment').click();
    const terraResp = await terraRespPromise;
    expect(terraResp.ok(), `terra HTTP ${terraResp.status()}`).toBeTruthy();
    const terraBody = await terraResp.json();
    expect(terraBody?.data?.status).toMatch(/^ok$/i);
    expect(terraBody?.data?.executed).not.toBe(true);
    await expectBankedModelResult(page, 'terra');

    // Board unchanged by model output.
    await expect(page.getByTestId('cop-revision')).toHaveAttribute('data-revision', revBefore!);
    await expect(page.getByTestId('cop-claim-row')).toHaveCount(claimCountBefore);

    // Sol briefing from bank (requires supervisor identity header from UI).
    await expect(page.getByTestId('request-briefing')).toBeEnabled();
    const solRespPromise = waitOperatorPOST(page, 'brief');
    await page.getByTestId('request-briefing').click();
    const solResp = await solRespPromise;
    expect(solResp.ok(), `sol HTTP ${solResp.status()} body=${await solResp.text().catch(() => '')}`).toBeTruthy();
    const solBody = await solResp.json();
    expect(solBody?.data?.status).toMatch(/^ok$/i);
    expect(solBody?.data?.executed).not.toBe(true);
    await expectBankedModelResult(page, 'sol');

    // Luna interpret on curated 911 beat (accepted after fixture enrichment).
    const lunaRespPromise = waitOperatorPOST(page, 'interpret');
    await page.getByTestId('interpret-event-baseline-01-911-call').click();
    const lunaResp = await lunaRespPromise;
    expect(lunaResp.ok(), `luna HTTP ${lunaResp.status()}`).toBeTruthy();
    const lunaBody = await lunaResp.json();
    expect(lunaBody?.data?.status).toMatch(/^ok$/i);
    await expectBankedModelResult(page, 'luna', { beat: 'baseline-01-911-call' });

    // Luna quarantine beat (fixture-08) — honest banked quarantine only.
    const qRespPromise = waitOperatorPOST(page, 'interpret');
    await page.getByTestId('interpret-event-fixture-08-quarantined-input').click();
    const qResp = await qRespPromise;
    expect(qResp.ok(), `luna quarantine HTTP ${qResp.status()}`).toBeTruthy();
    const qBody = await qResp.json();
    expect(String(qBody?.data?.status || '')).toMatch(/^quarantined$/i);
    await expect(page.getByTestId('model-result-card')).toHaveAttribute(
      'data-beat',
      'fixture-08-quarantined-input',
    );
    await expect(page.getByTestId('model-result-card')).toHaveAttribute('data-status', /^quarantined$/i);
    await expect(page.getByTestId('model-result-status')).toHaveAttribute('data-status', /^quarantined$/i);
    await expect(page.getByTestId('model-result-card')).toHaveAttribute('data-executed', 'false');
    await expect(page.getByTestId('model-provenance-badge')).toContainText(/replay \(banked\)/i);
    await expect(page.getByTestId('luna-quarantine-reason')).toBeVisible();

    // Final board still at rev 9.
    await expect(page.getByTestId('cop-revision')).toHaveAttribute('data-revision', revBefore!);
  });
});
