import { test, expect, type Page, type Response } from '@playwright/test';
import { playScenarioToRevision9, waitConnected } from './helpers';

async function waitOperatorPOST(page: Page, pathSuffix: string): Promise<Response> {
  return page.waitForResponse(
    (r) =>
      r.url().includes(`/api/v1/operator/${pathSuffix}`) &&
      r.request().method() === 'POST',
    { timeout: 30_000 },
  );
}

async function expectBankedModelResult(
  page: Page,
  agent: string,
  opts: { beat?: string; status?: RegExp } = {},
) {
  const statusRe = opts.status ?? /^(ok|accepted|repaired|quarantined|refused)$/i;
  await expect(page.getByTestId('model-result-card')).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId('model-result-card')).toHaveAttribute('data-agent', agent);
  await expect(page.getByTestId('model-result-card')).toHaveAttribute('data-executed', 'false');
  if (opts.beat) {
    await expect(page.getByTestId('model-result-card')).toHaveAttribute('data-beat', opts.beat);
  }
  await expect(page.getByTestId('model-result-status')).toHaveText(statusRe);
  await expect(page.getByTestId('model-result-boundary')).toHaveAttribute('data-executed', 'false');
}

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
    expect(terraBody?.data?.status).toMatch(/ok|accepted/i);
    expect(terraBody?.data?.executed).not.toBe(true);
    await expectBankedModelResult(page, 'terra');
    await expect(page.getByTestId('model-provenance-badge')).toContainText(
      /replay \(banked\)|banked|mosaic-fixture|fixture/i,
    );

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
    expect(solBody?.data?.status).toMatch(/ok|accepted/i);
    expect(solBody?.data?.executed).not.toBe(true);
    await expectBankedModelResult(page, 'sol');

    // Luna interpret on curated 911 beat.
    const lunaRespPromise = waitOperatorPOST(page, 'interpret');
    await page.getByTestId('interpret-event-baseline-01-911-call').click();
    const lunaResp = await lunaRespPromise;
    expect(lunaResp.ok(), `luna HTTP ${lunaResp.status()}`).toBeTruthy();
    const lunaBody = await lunaResp.json();
    expect(lunaBody?.data?.status).toMatch(/ok|accepted|repaired/i);
    await expectBankedModelResult(page, 'luna', { beat: 'baseline-01-911-call' });

    // Luna quarantine beat (fixture-08) — honest banked quarantine.
    const qRespPromise = waitOperatorPOST(page, 'interpret');
    await page.getByTestId('interpret-event-fixture-08-quarantined-input').click();
    const qResp = await qRespPromise;
    expect(qResp.ok(), `luna quarantine HTTP ${qResp.status()}`).toBeTruthy();
    const qBody = await qResp.json();
    expect(String(qBody?.data?.status || '')).toMatch(/quarantined|ok|accepted|repaired/i);
    await expect(page.getByTestId('model-result-card')).toHaveAttribute(
      'data-beat',
      'fixture-08-quarantined-input',
    );
    await expect(page.getByTestId('model-result-card')).toHaveAttribute('data-executed', 'false');

    // Final board still at rev 9.
    await expect(page.getByTestId('cop-revision')).toHaveAttribute('data-revision', revBefore!);
  });
});
