<script>
  import HelpTip from './HelpTip.svelte';

  let {
    providers,
    modelUsage = null,
    cassetteMode = 'passthrough'
  } = $props();

  let terraProvider = $derived(providers?.terra || 'fixture');
  let solProvider = $derived(providers?.sol || 'fixture');
  let lunaProvider = $derived(providers?.luna || 'fixture');

  let modeKey = $derived(normalizeCassetteMode(cassetteMode));
  let inferenceLabel = $derived(cassetteModeLabel(modeKey));

  // Only shown when the server reports a configured demo budget
  // (MOSAIC_DEMO_BUDGET_USD). Absent that, there is nothing meaningful to show.
  let hasBudget = $derived(modelUsage?.budget_usd !== undefined && modelUsage?.budget_usd !== null);
  let remainingLabel = $derived(formatUSD(modelUsage?.estimated_remaining_usd));

  function normalizeCassetteMode(raw) {
    const value = String(raw || '').trim().toLowerCase();
    if (value === 'replay' || value === 'recorded') return 'replay';
    if (value === 'record' || value === 'live') return 'record';
    return 'passthrough';
  }

  function cassetteModeLabel(key) {
    if (key === 'replay') return 'Replay';
    if (key === 'record') return 'Live (recording)';
    return 'Fixture';
  }

  function cassetteModeTip(key) {
    if (key === 'replay') {
      return 'Inference path: cassette Replay — process started with MOSAIC_SIM_MODE=replay. Terra/Sol serve banked responses (no paid API call). Mode is fixed at process start; UI “Refresh banked advice” only re-fetches, it does not re-bank or switch mode.';
    }
    if (key === 'record') {
      return 'Inference path: Live recording (MOSAIC_SIM_MODE=live). Live OpenAI calls are banked for later free replay after a restart with MOSAIC_SIM_MODE=replay.';
    }
    return 'Inference path: Fixture / demo pack (default). Pre-built scenario advice, not cassette replay of a prior live run.';
  }

  function formatUSD(value) {
    const number = Number(value);
    if (!Number.isFinite(number)) return '—';
    return number.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 2 });
  }

  function modeLabel(mode) {
    return mode === 'live' ? 'AI on' : 'Demo pack';
  }

  function modeTip(agent, mode) {
    if (agent === 'luna') {
      return mode === 'live'
        ? 'Luna (event reader) will call OpenAI when used. Still practice-only — no real feeds.'
        : 'Luna uses the pre-built demo pack (no OpenAI call). Fine for offline walkthroughs.';
    }
    if (agent === 'terra') {
      return mode === 'live'
        ? 'Terra (situation assessor) can call OpenAI when you refresh analysis. Suggestions never change the board by themselves.'
        : modeKey === 'replay'
          ? 'Terra uses banked cassette recordings in Replay mode (no OpenAI call).'
          : 'Terra shows pre-built demo assessments for this scenario (no OpenAI call).';
    }
    return mode === 'live'
      ? 'Sol (briefing helper) can call OpenAI only when you ask for a briefing. Never auto-sends.'
      : modeKey === 'replay'
        ? 'Sol uses banked cassette recordings in Replay mode (no OpenAI call).'
        : 'Sol uses the pre-built demo recommendation text (no OpenAI call).';
  }
</script>

<div class="model-modes-container" aria-label="AI mode for this demo">
  <div class="mode-indicator cassette-indicator" data-agent="cassette">
    <span class="agent-label">Inference</span>
    <span class="mode-badge" data-mode={modeKey}>
      {inferenceLabel}
    </span>
    <HelpTip text={cassetteModeTip(modeKey)} label="About inference mode" />
  </div>
  <div class="mode-indicator" data-agent="luna">
    <span class="agent-label">Luna · events</span>
    <span class="mode-badge" data-mode={lunaProvider}>
      {modeLabel(lunaProvider)}
    </span>
    <HelpTip text={modeTip('luna', lunaProvider)} label="About Luna" />
  </div>
  <div class="mode-indicator" data-agent="terra">
    <span class="agent-label">Terra · assess</span>
    <span class="mode-badge" data-mode={terraProvider}>
      {modeLabel(terraProvider)}
    </span>
    <HelpTip text={modeTip('terra', terraProvider)} label="About Terra" />
  </div>
  <div class="mode-indicator" data-agent="sol">
    <span class="agent-label">Sol · brief</span>
    <span class="mode-badge" data-mode={solProvider}>
      {modeLabel(solProvider)}
    </span>
    <HelpTip text={modeTip('sol', solProvider)} label="About Sol" />
  </div>
  {#if hasBudget}
    <div class="mode-indicator budget-indicator" data-agent="budget">
      <span class="mode-badge budget-badge">~${remainingLabel} left (est.)</span>
      <HelpTip
        text="Rough estimate of demo budget remaining, computed from this server session's token usage. Not your real OpenAI balance; resets when the server restarts."
        label="About estimated budget remaining"
      />
    </div>
  {/if}
</div>
