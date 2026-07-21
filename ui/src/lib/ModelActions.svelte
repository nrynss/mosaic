<script>
  import { onMount } from 'svelte';
  import HelpTip from './HelpTip.svelte';
  import ModelResultCard from './ModelResultCard.svelte';

  /** Curated Luna beats for the primary narrative; remaining beats stay available. */
  const CURATED_BEAT_IDS = [
    'baseline-01-911-call',
    'baseline-04-road-closure',
    'baseline-05-ems-availability',
    'fixture-07-repaired-incomplete-road',
    'fixture-08-quarantined-input',
    'fixture-10-road-correction'
  ];

  let {
    readEnvelope,
    loadAdvisories,
    cassetteModeHint = '',
    /** Live COP state_revision from the workspace (number or undefined). */
    copRevision = null
  } = $props();

  let loadState = $state('idle'); // idle | loading | ready | error
  let loadError = $state('');
  let interactions = $state(null);

  let actionState = $state('idle'); // idle | loading | ready | error
  let actionMessage = $state('');
  let lastResult = $state(null);
  let lastError = $state('');
  let lastAgent = $state('');
  let lastBeatId = $state('');

  let selectedOtherBeat = $state('');
  let showAllBeats = $state(false);

  let cassetteMode = $derived(
    interactions?.cassette_mode || cassetteModeHint || ''
  );
  let supervisorIdentity = $derived(
    interactions?.supervisor_identity || 'supervisor-demo'
  );
  let terraStep = $derived(findStep(interactions, 'terra'));
  let solStep = $derived(findStep(interactions, 'sol'));
  let lunaSteps = $derived(
    Array.isArray(interactions?.steps)
      ? interactions.steps.filter((s) => s.kind === 'luna')
      : []
  );
  let curatedLuna = $derived(
    lunaSteps.filter((s) => CURATED_BEAT_IDS.includes(s.beat_id))
  );
  let otherLuna = $derived(
    lunaSteps.filter((s) => !CURATED_BEAT_IDS.includes(s.beat_id))
  );
  let expectedCOP = $derived(Number(interactions?.expected_cop_revision) || 9);
  let liveCOP = $derived(
    copRevision === null || copRevision === undefined || copRevision === ''
      ? null
      : Number(copRevision)
  );
  /** Terra/Sol bank keys embed revN — only enable when COP matches expected. */
  let copReadyForModel = $derived(liveCOP !== null && !Number.isNaN(liveCOP) && liveCOP === expectedCOP);

  onMount(() => {
    void loadInteractions();
  });

  function findStep(doc, kind) {
    if (!doc || !Array.isArray(doc.steps)) return null;
    return doc.steps.find((s) => s.kind === kind) || null;
  }

  async function loadInteractions() {
    loadState = 'loading';
    loadError = '';
    try {
      const doc = await readEnvelope('demo/interactions');
      interactions = doc;
      loadState = 'ready';
      const others = Array.isArray(doc?.steps)
        ? doc.steps.filter((s) => s.kind === 'luna' && !CURATED_BEAT_IDS.includes(s.beat_id))
        : [];
      if (!selectedOtherBeat && others.length > 0) {
        selectedOtherBeat = others[0].beat_id;
      }
    } catch (err) {
      loadState = 'error';
      loadError = err?.message || String(err);
      interactions = null;
    }
  }

  async function postOperator(step, agent, beatId = '') {
    if (!step?.endpoint || !step?.request) {
      lastError = 'Interaction payload is not available.';
      lastResult = null;
      lastAgent = agent;
      lastBeatId = beatId;
      actionState = 'error';
      return;
    }
    actionState = 'loading';
    actionMessage = `Running ${agent}…`;
    lastError = '';
    lastResult = null;
    lastAgent = agent;
    lastBeatId = beatId;

    const headers = { 'Content-Type': 'application/json' };
    // Sol requires the supervisor demo identity at the API boundary.
    if (agent === 'sol') {
      headers['X-Mosaic-Demo-Identity'] = supervisorIdentity;
    }

    try {
      const data = await readEnvelope(step.endpoint, {
        method: 'POST',
        headers,
        body: JSON.stringify(step.request)
      });
      lastResult = data;
      actionState = 'ready';
      actionMessage = `${agent} finished · status ${data?.status || 'unknown'} · executed: ${data?.executed === true}`;
      if (typeof loadAdvisories === 'function') {
        try {
          await loadAdvisories();
        } catch {
          // History refresh is best-effort; primary result still shows.
        }
      }
    } catch (err) {
      lastError = err?.message || String(err);
      lastResult = null;
      actionState = 'error';
      actionMessage = lastError;
    }
  }

  function runTerra() {
    return postOperator(terraStep, 'terra');
  }

  function runSol() {
    return postOperator(solStep, 'sol');
  }

  function runLuna(step) {
    return postOperator(step, 'luna', step?.beat_id || '');
  }

  function runSelectedOther() {
    const step = otherLuna.find((s) => s.beat_id === selectedOtherBeat);
    if (step) return runLuna(step);
  }

  function beatLabel(step) {
    const base = step.beat_id || step.raw_event_ref || 'event';
    if (step.expected_status === 'quarantined') return `${base} (expect quarantine)`;
    return base;
  }
</script>

<section class="model-actions" aria-label="Operator model actions">
  <div class="panel-section-header">
    <span class="eyebrow">
      Ask the models
      <HelpTip
        text="These buttons POST the exact demo recording-manifest payloads. In replay mode you see banked real output at $0; in fixture mode Terra/Sol honestly decline; live calls OpenAI when configured. Nothing they return changes the board."
        label="About model actions"
      />
    </span>
    <h3>Generate assessment · briefing · interpret</h3>
    <p class="section-desc">
      Payloads come from <code>GET /api/v1/demo/interactions</code> so replay hits the cassette bank.
      Results are <strong>proposed only</strong> — the board stays on scenario facts.
    </p>
  </div>

  {#if loadState === 'loading'}
    <p class="hint" aria-live="polite">Loading demo interactions…</p>
  {:else if loadState === 'error'}
    <p class="problem" role="alert">Could not load demo interactions: {loadError}</p>
    <button type="button" class="action-btn secondary" onclick={loadInteractions}>Retry</button>
  {:else if loadState === 'ready'}
    <div class="workspace-actions">
      <button
        type="button"
        class="action-btn primary"
        data-testid="generate-assessment"
        disabled={actionState === 'loading' || !terraStep || !copReadyForModel}
        title={!copReadyForModel
          ? `Play the scenario until COP rev ${expectedCOP} (current: ${liveCOP ?? '—'})`
          : 'Generate Terra assessment from the demo manifest'}
        onclick={runTerra}
      >
        Generate assessment (Terra)
      </button>
      <button
        type="button"
        class="action-btn primary"
        data-testid="request-briefing"
        disabled={actionState === 'loading' || !solStep || !copReadyForModel}
        title={!copReadyForModel
          ? `Play the scenario until COP rev ${expectedCOP} (current: ${liveCOP ?? '—'})`
          : 'Request Sol briefing from the demo manifest'}
        onclick={runSol}
      >
        Request briefing (Sol)
      </button>
    </div>
    <p class="hint subtle" class:problem={!copReadyForModel && loadState === 'ready'}>
      {#if copReadyForModel}
        COP rev {liveCOP} matches Terra/Sol bank (rev {expectedCOP}). Mode: <code>{cassetteMode || 'unknown'}</code>
      {:else}
        Terra/Sol disabled until COP rev {expectedCOP} (current: {liveCOP ?? '—'}) — play the scenario first.
        Mode: <code>{cassetteMode || 'unknown'}</code>
      {/if}
    </p>

    <div class="luna-block">
      <div class="luna-header">
        <span class="eyebrow">Luna · interpret event</span>
        <HelpTip
          text="Interprets a beat’s exact synthetic raw event. Quarantines are shown proudly — that is the anti-fabrication boundary."
          label="About Luna interpret"
        />
      </div>
      <div class="curated-grid">
        {#each curatedLuna as step (step.beat_id)}
          <button
            type="button"
            class="action-btn luna-btn"
            data-testid={`interpret-event-${step.beat_id}`}
            data-beat={step.beat_id}
            disabled={actionState === 'loading'}
            onclick={() => runLuna(step)}
          >
            {beatLabel(step)}
          </button>
        {/each}
      </div>

      {#if otherLuna.length > 0}
        <div class="other-beats">
          <button
            type="button"
            class="linkish"
            aria-expanded={showAllBeats}
            onclick={() => (showAllBeats = !showAllBeats)}
          >
            {showAllBeats ? 'Hide other beats' : `Show all beats (${otherLuna.length} more)`}
          </button>
          {#if showAllBeats}
            <div class="other-row">
              <label for="other-luna-beat">Other beat</label>
              <select id="other-luna-beat" bind:value={selectedOtherBeat}>
                {#each otherLuna as step (step.beat_id)}
                  <option value={step.beat_id}>{beatLabel(step)}</option>
                {/each}
              </select>
              <button
                type="button"
                class="action-btn luna-btn"
                data-testid={selectedOtherBeat
                  ? `interpret-event-${selectedOtherBeat}`
                  : 'interpret-event-other'}
                data-beat={selectedOtherBeat || undefined}
                disabled={actionState === 'loading' || !selectedOtherBeat}
                onclick={runSelectedOther}
              >
                Interpret selected
              </button>
            </div>
          {/if}
        </div>
      {/if}
    </div>
  {/if}

  {#if actionMessage}
    <p class="action-status" aria-live="polite">{actionMessage}</p>
  {/if}

  <ModelResultCard
    result={lastResult}
    agent={lastAgent}
    beatId={lastBeatId}
    cassetteMode={cassetteMode}
    error={lastError}
  />
</section>

<style>
  .model-actions {
    display: grid;
    gap: 0.65rem;
    margin-top: 0.75rem;
    padding: 0.7rem;
    background: var(--bg1);
    border: 1px solid var(--line);
  }

  .panel-section-header {
    border-bottom: 1px solid var(--line-strong);
    padding-bottom: 0.4rem;
  }

  .panel-section-header h3 {
    font-size: 0.85rem;
    color: var(--ink);
    margin: 0.2rem 0;
  }

  .section-desc {
    font-size: 0.68rem;
    color: var(--ink-dim);
    margin: 0;
    line-height: 1.45;
  }

  .eyebrow {
    font-family: var(--mono);
    font-size: 0.56rem;
    font-weight: 700;
    letter-spacing: 0.08em;
    text-transform: uppercase;
    color: var(--ink-faint);
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
  }

  .workspace-actions {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.4rem;
  }

  .action-btn {
    padding: 0.45rem 0.5rem;
    color: var(--ink);
    background: transparent;
    border: 1px solid var(--line-strong);
    font-family: var(--mono);
    font-weight: 700;
    font-size: 0.6rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
  }

  .action-btn.primary:hover:not(:disabled) {
    border-color: var(--amber);
    color: var(--amber);
  }

  .action-btn.secondary {
    justify-self: start;
  }

  .action-btn.luna-btn {
    text-transform: none;
    letter-spacing: 0.02em;
    font-size: 0.58rem;
    text-align: left;
  }

  .action-btn.luna-btn:hover:not(:disabled) {
    border-color: var(--info);
    color: var(--info);
  }

  .hint {
    margin: 0;
    font-size: 0.64rem;
    color: var(--ink-dim);
    font-family: var(--mono);
  }

  .hint.subtle {
    color: var(--ink-faint);
  }

  .hint.problem {
    color: var(--warn);
  }

  .problem {
    margin: 0;
    font-size: 0.68rem;
    color: var(--alert);
  }

  .luna-block {
    display: grid;
    gap: 0.4rem;
    padding-top: 0.25rem;
    border-top: 1px dashed var(--line);
  }

  .luna-header {
    display: flex;
    align-items: center;
    gap: 0.3rem;
  }

  .curated-grid {
    display: grid;
    gap: 0.3rem;
  }

  .other-beats {
    display: grid;
    gap: 0.35rem;
  }

  .linkish {
    justify-self: start;
    background: transparent;
    border: none;
    color: var(--info);
    font-family: var(--mono);
    font-size: 0.6rem;
    text-decoration: underline;
    padding: 0;
  }

  .other-row {
    display: grid;
    grid-template-columns: auto 1fr auto;
    gap: 0.35rem;
    align-items: center;
  }

  .other-row label {
    font-size: 0.58rem;
    font-weight: 700;
    color: var(--ink-dim);
  }

  .other-row select {
    font-size: 0.66rem;
    padding: 0.3rem 0.35rem;
    border: 1px solid var(--line-strong);
    background: var(--bg0);
    color: var(--ink);
  }

  .action-status {
    margin: 0;
    font-size: 0.62rem;
    font-family: var(--mono);
    color: var(--ink-faint);
  }

  @media (max-width: 720px) {
    .workspace-actions {
      grid-template-columns: 1fr;
    }
    .other-row {
      grid-template-columns: 1fr;
    }
  }
</style>
