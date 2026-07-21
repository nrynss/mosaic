import { expect, type Page, type Response } from '@playwright/test';

/** Wait until SSE connection pill reports live. */
export async function waitConnected(page: Page) {
  await expect(page.getByTestId('connection-pill')).toHaveAttribute('data-state', 'live', {
    timeout: 30_000,
  });
}

function isSimulationAPI(url: string, suffix: string) {
  return url.includes(`/api/v1/simulation/${suffix}`);
}

/**
 * Reset session then play scenario until COP revision 9 with end-state facts.
 *
 * Uses simulation HTTP responses (not fixed timeouts) so a shared webServer
 * cannot leave us asserting a stale board mid workspace_clear.
 */
export async function playScenarioToRevision9(page: Page) {
  await waitConnected(page);

  await Promise.all([
    page.waitForResponse(
      (r) => isSimulationAPI(r.url(), 'reset') && r.request().method() === 'POST' && r.ok(),
      { timeout: 20_000 },
    ),
    page.getByTestId('start-over').click(),
  ]);
  await expect(page.getByTestId('play-scenario')).toBeEnabled({ timeout: 15_000 });

  await Promise.all([
    page.waitForResponse(
      (r) => isSimulationAPI(r.url(), 'start') && r.request().method() === 'POST' && r.ok(),
      { timeout: 20_000 },
    ),
    page.getByTestId('play-scenario').click(),
  ]);

  // Stable end state: session ended, COP rev 9, Brook Lane open, multiple facts.
  await expect
    .poll(
      async () => {
        const rev = await page.getByTestId('cop-revision').getAttribute('data-revision');
        const status = await page.getByTestId('run-status').getAttribute('data-status');
        const claims = await page.getByTestId('cop-claim-row').count();
        const brookStatus = await page
          .locator('[data-testid="cop-claim-row"][data-entity-id="road-brook-lane"]')
          .getAttribute('data-status')
          .catch(() => '');
        return {
          status,
          rev,
          claims,
          brookOpen: String(brookStatus || '').toLowerCase() === 'open',
        };
      },
      { timeout: 45_000, intervals: [50, 100, 200, 500] },
    )
    .toMatchObject({
      status: 'ended',
      rev: '9',
      brookOpen: true,
    });

  await expect(page.getByTestId('cop-claim-row').first()).toBeVisible();
  const claims = await page.getByTestId('cop-claim-row').count();
  expect(claims).toBeGreaterThanOrEqual(5);
}

export function claimRow(page: Page, kind: string, entityId: string) {
  return page.locator(
    `[data-testid="cop-claim-row"][data-kind="${kind}"][data-entity-id="${entityId}"]`,
  );
}

export function insightCard(page: Page, insightId: string) {
  return page.locator(`[data-testid="advice-insight-card"][data-insight-id="${insightId}"]`);
}

export function recommendationCard(page: Page, recommendationId: string) {
  return page.locator(
    `[data-testid="advice-recommendation-card"][data-recommendation-id="${recommendationId}"]`,
  );
}

/**
 * Deliberate on-camera hold so the recorded walkthrough leaves room for
 * voiceover. Regression specs never call this (they wait on state); only the
 * `record` project paces itself with holds. Duration is tunable via
 * MOSAIC_E2E_HOLD_MS so the video can be lengthened without editing specs.
 */
export const HOLD_MS = Number(process.env.MOSAIC_E2E_HOLD_MS || 2600);

export async function hold(page: Page, ms: number = HOLD_MS) {
  await page.waitForTimeout(ms);
}

/** Await the operator model POST (analyze | brief | interpret). */
export function waitOperatorPOST(
  page: Page,
  pathSuffix: string,
  timeout = 30_000,
): Promise<Response> {
  return page.waitForResponse(
    (r) =>
      r.url().includes(`/api/v1/operator/${pathSuffix}`) && r.request().method() === 'POST',
    { timeout },
  );
}

/**
 * Assert the model result card rendered a successful banked (replay) result for
 * `agent`, honestly marked executed:false and provenance "replay (banked)".
 */
export async function expectBankedModelResult(
  page: Page,
  agent: string,
  opts: { beat?: string; status?: RegExp } = {},
) {
  const statusRe = opts.status ?? /^(ok|accepted|repaired)$/i;
  await expect(page.getByTestId('model-result-card')).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId('model-result-card')).toHaveAttribute('data-agent', agent);
  await expect(page.getByTestId('model-result-card')).toHaveAttribute('data-executed', 'false');
  if (opts.beat) {
    await expect(page.getByTestId('model-result-card')).toHaveAttribute('data-beat', opts.beat);
  }
  await expect(page.getByTestId('model-result-status')).toHaveText(statusRe);
  await expect(page.getByTestId('model-result-status')).toHaveAttribute('data-status', statusRe);
  await expect(page.getByTestId('model-result-boundary')).toHaveAttribute('data-executed', 'false');
  // Successful bank hits under replay must not be mislabeled as generic fixture.
  await expect(page.getByTestId('model-provenance-badge')).toContainText(/replay \(banked\)/i);
}
