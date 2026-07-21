import { test, expect, type Page } from '@playwright/test';
import {
  claimRow,
  expectBankedModelResult,
  hold,
  HOLD_MS,
  insightCard,
  waitConnected,
  waitOperatorPOST,
} from './helpers';

// A longer linger for content-heavy beats (the console receipts) so the
// voiceover has room to read. Scales with MOSAIC_E2E_HOLD_MS via HOLD_MS.
const HOLD_LONG_MS = HOLD_MS * 2;

// 'replay' (banked, deterministic) or 'live' (real OpenAI, non-deterministic).
// Under live we cannot assert banked provenance or a fixed Luna outcome — we
// only assert that real model output rendered.
const RECORD_MODE = (process.env.MOSAIC_E2E_RECORD_MODE || 'replay').toLowerCase();
const LIVE = RECORD_MODE === 'live';
// Real inference is slower than a bank hit; give live operator calls more room.
const OPERATOR_TIMEOUT = LIVE ? 90_000 : 30_000;

/**
 * Assert a model result rendered for `agent`. Replay: strict banked provenance
 * (delegates to expectBankedModelResult). Live: real output is non-deterministic,
 * so only assert the card + status pill appeared for the right agent (longer
 * timeout for real inference latency).
 */
async function expectModelResult(
  page: Page,
  agent: string,
  opts: { beat?: string; status?: RegExp } = {},
) {
  if (!LIVE) {
    await expectBankedModelResult(page, agent, opts);
    return;
  }
  await expect(page.getByTestId('model-result-card')).toBeVisible({ timeout: 90_000 });
  await expect(page.getByTestId('model-result-card')).toHaveAttribute('data-agent', agent);
  if (opts.beat) {
    await expect(page.getByTestId('model-result-card')).toHaveAttribute('data-beat', opts.beat);
  }
  await expect(page.getByTestId('model-result-status')).toBeVisible();
}

/**
 * Slow, single, progressive play for the recording.
 *
 * The board starts empty (MOSAIC_SEED_ON_START=0), so Play is enabled from a
 * pending session — no reset needed. (Note: "Start over"/Reset auto-starts a
 * fresh run, so it is NOT a way to pause on an empty board.) With the record
 * server's ~2.6s beat spacing, the poll below runs while the COP fills in one
 * beat at a time — the honest reveal the camera captures.
 */
async function playScenarioSlow(page: Page) {
  await expect(page.getByTestId('play-scenario')).toBeEnabled({ timeout: 15_000 });
  await Promise.all([
    page.waitForResponse(
      (r) =>
        r.url().includes('/api/v1/simulation/start') &&
        r.request().method() === 'POST' &&
        r.ok(),
      { timeout: 20_000 },
    ),
    page.getByTestId('play-scenario').click(),
  ]);

  await expect
    .poll(
      async () => {
        const rev = await page.getByTestId('cop-revision').getAttribute('data-revision');
        const status = await page.getByTestId('run-status').getAttribute('data-status');
        const brook = await page
          .locator('[data-testid="cop-claim-row"][data-entity-id="road-brook-lane"]')
          .getAttribute('data-status')
          .catch(() => '');
        return { status, rev, brookOpen: String(brook || '').toLowerCase() === 'open' };
      },
      // Live runs live Luna per beat during play, so allow much more headroom.
      { timeout: LIVE ? 300_000 : 90_000, intervals: [250, 500, 1000] },
    )
    .toMatchObject({ status: 'ended', rev: '9', brookOpen: true });
}

/**
 * Full demo narrative as a single recording — fixture board reveal PLUS the
 * live player (banked Terra / Sol / Luna model output). Runs in the `record`
 * project only: replay mode against the cassette bank, video always on, natural
 * motion, 1920×1080, and a slow server beat spacing so the progressive COP
 * reveal is narratable.
 *
 * Pacing philosophy: the video is intentionally slow and methodical so a
 * voiceover can land on each beat. Post-process (speed up / trim) as needed for
 * the final cut — holds are tunable via MOSAIC_E2E_HOLD_MS.
 *
 * This is a recording aid, not a fast regression gate; `replay-model-actions`
 * remains the tight assertion suite for the same flow.
 */
test.describe('Replay demo walkthrough (recorded)', () => {
  // Slow server spacing + deliberate holds make this long by design; live adds
  // real per-beat + operator inference latency on top.
  test.setTimeout(LIVE ? 600_000 : 360_000);

  test('empty board → replay mode → play → COP → advice → live player → handoffs → history', async ({
    page,
  }) => {
    // 1. Cold open: the operator console, connected and empty, waiting for Play.
    //    ai-mode-pill reflects whether a key is configured: 'live' vs 'idle'.
    await page.goto('/');
    await waitConnected(page);
    await expect(page.getByTestId('ai-mode-pill')).toHaveAttribute(
      'data-state',
      LIVE ? 'live' : 'idle',
    );
    await hold(page);

    // 2. Establish the mode up front (replay = banked $0; live = real OpenAI).
    await page.getByTestId('model-actions').scrollIntoViewIfNeeded();
    await expect(page.getByTestId('model-actions')).toBeVisible();
    await expect(page.getByTestId('model-cassette-mode')).toHaveText(
      LIVE ? /record|live/i : /replay/i,
      { timeout: 20_000 },
    );
    // The live-player controls are gated until the incident picture is complete.
    await expect(page.getByTestId('generate-assessment')).toBeDisabled();
    await expect(page.getByTestId('request-briefing')).toBeDisabled();
    await hold(page);

    // 3. Play the scenario. The slow server beat spacing means the COP fills in
    //    progressively, on camera, one beat at a time — the honest reveal.
    await playScenarioSlow(page);
    await hold(page);

    // 4. COP walk: linger on the facts the voiceover calls out.
    const incident = claimRow(page, 'Incident', 'incident-domestic-001');
    await incident.scrollIntoViewIfNeeded();
    await expect(incident).toBeVisible();
    await hold(page);

    const brookLane = claimRow(page, 'Road', 'road-brook-lane');
    await brookLane.scrollIntoViewIfNeeded();
    await expect(brookLane).toHaveAttribute('data-status', /^open$/i);
    await hold(page);

    // Snapshot the board so we can prove the live player never mutates it.
    const revAfterPlay = await page.getByTestId('cop-revision').getAttribute('data-revision');
    const claimsAfterPlay = await page.getByTestId('cop-claim-row').count();
    expect(revAfterPlay).toBe('9');
    expect(claimsAfterPlay).toBeGreaterThan(0);

    // 5. Advice + supersession: the rev-7 access insight, now not-current at rev 9.
    await page.getByTestId('refresh-advice').click();
    const accessInsight = insightCard(page, 'insight-domestic-access-001');
    await accessInsight.scrollIntoViewIfNeeded();
    await expect(accessInsight).toBeVisible();
    await expect(accessInsight).toHaveAttribute(
      'data-status',
      /superseded|not_current|obsolete/,
    );
    await hold(page);

    // 6. The operator asks the models for recommendations — Terra assessment first.
    await page.getByTestId('model-actions').scrollIntoViewIfNeeded();
    await expect(page.getByTestId('model-cop-gate')).toHaveAttribute('data-ready', 'true');
    await expect(page.getByTestId('generate-assessment')).toBeEnabled();
    const terraResp = await Promise.all([
      waitOperatorPOST(page, 'analyze', OPERATOR_TIMEOUT),
      page.getByTestId('generate-assessment').click(),
    ]).then(([r]) => r);
    expect(terraResp.ok(), `terra HTTP ${terraResp.status()}`).toBeTruthy();
    await expectModelResult(page, 'terra');
    await hold(page);
    // Advice augments the picture; it does not overwrite the operating board.
    await expect(page.getByTestId('cop-revision')).toHaveAttribute('data-revision', revAfterPlay!);
    await expect(page.getByTestId('cop-claim-row')).toHaveCount(claimsAfterPlay);

    // 7. LIVE PLAYER — Sol supervisor briefing.
    const solResp = await Promise.all([
      waitOperatorPOST(page, 'brief', OPERATOR_TIMEOUT),
      page.getByTestId('request-briefing').click(),
    ]).then(([r]) => r);
    expect(solResp.ok(), `sol HTTP ${solResp.status()}`).toBeTruthy();
    await expectModelResult(page, 'sol');
    await hold(page);

    // 8. LIVE PLAYER — Luna interprets the 911 call (accepted after enrichment)…
    const lunaResp = await Promise.all([
      waitOperatorPOST(page, 'interpret', OPERATOR_TIMEOUT),
      page.getByTestId('interpret-event-baseline-01-911-call').click(),
    ]).then(([r]) => r);
    expect(lunaResp.ok(), `luna HTTP ${lunaResp.status()}`).toBeTruthy();
    await expectModelResult(page, 'luna', { beat: 'baseline-01-911-call' });
    await hold(page);

    // …and the malformed payload (beat 8). Replay banks a quarantine; live is the
    // real model's call, so assert the reason only when it actually quarantines.
    const qResp = await Promise.all([
      waitOperatorPOST(page, 'interpret', OPERATOR_TIMEOUT),
      page.getByTestId('interpret-event-fixture-08-quarantined-input').click(),
    ]).then(([r]) => r);
    expect(qResp.ok(), `luna quarantine HTTP ${qResp.status()}`).toBeTruthy();
    await expect(page.getByTestId('model-result-card')).toHaveAttribute(
      'data-beat',
      'fixture-08-quarantined-input',
    );
    if (LIVE) {
      await expect(page.getByTestId('model-result-status')).toBeVisible();
    } else {
      await expect(page.getByTestId('model-result-status')).toHaveAttribute(
        'data-status',
        /^quarantined$/i,
      );
      await expect(page.getByTestId('luna-quarantine-reason')).toBeVisible();
    }
    await hold(page);

    // 8.5 The operator records their call on the model's assessment: pull it into
    //     the decision panel with "Use in my decision", type a note, and approve.
    const decisionInsight = insightCard(page, 'insight-domestic-access-001');
    await decisionInsight.scrollIntoViewIfNeeded();
    await decisionInsight.getByTestId('advice-insight-use-decision').click();
    await expect(page.getByTestId('decision-target')).toHaveValue('insight-domestic-access-001');
    await expect(page.getByTestId('decision-kind')).toHaveValue('insight');
    await page.getByTestId('decision-panel').scrollIntoViewIfNeeded();
    const decisionNote =
      'Reviewed: access warning is out of date now that Brook Lane has reopened — no dispatch action needed.';
    await page.getByTestId('decision-note').click();
    await page.getByTestId('decision-note').pressSequentially(decisionNote, { delay: 28 });
    await page.getByTestId('decision-approve').click();
    await expect(page.getByTestId('decision-result')).toBeVisible();
    await expect(page.getByTestId('decision-audit-id')).toBeVisible();
    await hold(page);

    // 9. The operator acts on the models' advice: forwards the recommendation to
    //    Dispatch and logs a maintenance follow-up — typed on camera so the human
    //    doing the work is the story.
    const dispatchNote =
      'Unit 17 on scene at Cedar Lane. Brook Lane cleared and open — route EMS-4 via Brook Lane; Main Street bridge remains blocked.';
    await page.getByTestId('dispatch-note').scrollIntoViewIfNeeded();
    await page.getByTestId('dispatch-note').click();
    await page.getByTestId('dispatch-note').pressSequentially(dispatchNote, { delay: 28 });
    await page.getByTestId('dispatch-save').click();
    await expect(page.getByTestId('dispatch-result')).toBeVisible();
    await hold(page);

    const maintenanceNote =
      'Brook Lane reopened after debris clearance. Confirm Main Street bridge closure signage and log for follow-up inspection.';
    await page.getByTestId('maintenance-note').click();
    await page.getByTestId('maintenance-note').pressSequentially(maintenanceNote, { delay: 28 });
    await page.getByTestId('maintenance-save').click();
    await expect(page.getByTestId('maintenance-result')).toBeVisible();
    await hold(page);

    // 10. Decision history: every action left an auditable trail.
    await page.getByTestId('refresh-advice').click();
    await page.getByTestId('tab-decision-history').click();
    await expect(page.getByTestId('decision-history')).toBeVisible();
    await expect(
      page.locator('[data-testid="audit-record-row"]').filter({ hasText: dispatchNote }),
    ).toBeVisible({ timeout: 20_000 });
    await expect(
      page.locator('[data-testid="audit-record-row"]').filter({ hasText: maintenanceNote }),
    ).toBeVisible();
    await hold(page);

    // 11. Closing receipts: expand the Developer Console & Status drawer to show
    //     the durable ledger, Luna lifecycle, and model-run counts behind the demo.
    await expect(page.getByTestId('developer-console')).toHaveAttribute('data-collapsed', 'true');
    await page.getByTestId('developer-console-toggle').scrollIntoViewIfNeeded();
    await page.getByTestId('developer-console-toggle').click();
    await expect(page.getByTestId('developer-console')).toHaveAttribute('data-collapsed', 'false');
    await expect(page.getByTestId('developer-console-content')).toBeVisible();
    await expect(page.getByTestId('ledger-counts')).toBeVisible();
    // Linger on the receipts as the closing beat so the voiceover can read the
    // ledger / Luna lifecycle / model-run counts. (Board-unchanged honesty is
    // already asserted after Terra, above, while on the Live Incident Board tab.)
    await hold(page, HOLD_LONG_MS);
    await hold(page);
  });
});
