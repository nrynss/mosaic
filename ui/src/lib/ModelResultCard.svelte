<script>
  /**
   * Honest rendering of one operator model response (Terra / Sol / Luna).
   * Never claims board mutation; surfaces refusals and quarantines.
   */
  let {
    result = null,
    agent = '',
    beatId = '',
    cassetteMode = '',
    error = ''
  } = $props();

  let status = $derived(result?.status || (error ? 'error' : ''));
  let executed = $derived(result?.executed === true);
  let modelRun = $derived(result?.model_run || null);
  let insight = $derived(result?.insight || null);
  let recommendation = $derived(result?.recommendation || null);
  let lunaResult = $derived(result?.luna_result || null);
  let quarantineReason = $derived(
    lunaResult?.reason ||
      modelRun?.failure_detail ||
      (status === 'quarantined' ? 'Quarantined without a reason field' : '')
  );

  let provenance = $derived(buildProvenance(cassetteMode, modelRun, result?.providers, agent));
  let statusTone = $derived(statusClass(status));

  function buildProvenance(mode, run, providers, agentName) {
    const provider =
      run?.provider ||
      (providers && agentName ? providers[agentName] : '') ||
      '';
    const model = run?.model || '';
    const modeLabel = modeLabelFor(mode, provider);
    const bits = [modeLabel];
    if (provider) bits.push(provider);
    if (model) bits.push(model);
    return bits.filter(Boolean).join(' · ');
  }

  function modeLabelFor(mode, provider) {
    const m = String(mode || '').toLowerCase();
    if (m === 'replay') return 'replay (banked)';
    if (m === 'record') return 'record';
    if (provider === 'openai' || m === 'live') return 'live';
    if (provider === 'mosaic-fixture' || provider === 'fixture' || m === 'passthrough' || m === 'fixture') {
      return 'fixture';
    }
    if (m) return m;
    return provider || 'unknown';
  }

  function statusClass(value) {
    switch (String(value || '').toLowerCase()) {
      case 'ok':
      case 'accepted':
      case 'repaired':
        return 'ok';
      case 'quarantined':
      case 'refused':
      case 'rejected':
      case 'invalid':
        return 'warn';
      case 'error':
      case 'failed':
      case 'unavailable':
      case 'timed_out':
        return 'alert';
      default:
        return 'info';
    }
  }

  function arrayOf(value) {
    return Array.isArray(value) ? value : [];
  }
</script>

{#if result || error}
  <article class="model-result-card" data-testid="model-result-card" data-agent={agent} data-beat={beatId || undefined}>
    <header class="result-header">
      <div class="result-title-row">
        <span class="agent-tag">{agent || 'model'}</span>
        {#if beatId}
          <code class="beat-id">{beatId}</code>
        {/if}
        <span
          class="status-pill"
          data-tone={statusTone}
          data-testid="model-result-status"
        >
          {status || 'unknown'}
        </span>
      </div>
      <span class="provenance-badge" data-testid="model-provenance-badge" title="How this output was produced">
        {provenance}
      </span>
    </header>

    <p class="boundary-line" class:executed={executed}>
      {#if executed}
        Unexpected: marked executed. Board should never change from model output.
      {:else}
        Proposed, not applied — board unchanged · executed: false
      {/if}
    </p>

    {#if error}
      <p class="problem" role="alert">{error}</p>
    {/if}

    {#if modelRun?.failure_detail && status !== 'ok'}
      <p class="detail-block">
        <strong>Detail</strong>
        {modelRun.failure_detail}
      </p>
    {/if}

    {#if insight}
      <div class="payload-block insight-block">
        <h4>Assessment (Insight)</h4>
        <p class="id-line"><code>{insight.insight_id}</code></p>
        <ul>
          {#each arrayOf(insight.assertions) as assertion}
            <li>{assertion}</li>
          {/each}
        </ul>
        {#if insight.confidence}
          <p class="confidence">
            Confidence: {insight.confidence.basis}
            (sources: {insight.confidence.source_quality}, reasoning: {insight.confidence.reasoning_support})
          </p>
        {/if}
      </div>
    {/if}

    {#if recommendation}
      <div class="payload-block rec-block">
        <h4>Briefing (Recommendation)</h4>
        <p class="id-line"><code>{recommendation.recommendation_id}</code></p>
        <p class="rec-text">{recommendation.text}</p>
      </div>
    {/if}

    {#if lunaResult}
      <div class="payload-block luna-block">
        <h4>Luna result</h4>
        <p class="id-line">
          <code>{lunaResult.luna_result_id}</code>
          · status <strong>{lunaResult.status}</strong>
        </p>
        {#if result?.canonical_event_id || lunaResult.canonical_event_id}
          <p class="id-line">
            Canonical event: <code>{result?.canonical_event_id || lunaResult.canonical_event_id}</code>
          </p>
        {/if}
        {#if lunaResult.status === 'quarantined' || status === 'quarantined' || quarantineReason}
          <p class="quarantine-reason" data-testid="luna-quarantine-reason">
            <strong>Quarantine reason:</strong> {quarantineReason || lunaResult.reason || '—'}
          </p>
        {/if}
      </div>
    {:else if (status === 'quarantined' || result?.result_status === 'quarantined') && quarantineReason}
      <p class="quarantine-reason" data-testid="luna-quarantine-reason">
        <strong>Quarantine reason:</strong> {quarantineReason}
      </p>
    {/if}

    {#if status === 'refused' && !insight && !recommendation && !lunaResult}
      <p class="detail-block">
        Interactive assessment declined
        {#if cassetteMode === 'passthrough' || cassetteMode === 'fixture' || !cassetteMode}
          (fixture path — honest refuse, not a live model answer).
        {:else}
          .
        {/if}
      </p>
    {/if}

    {#if result?.audit_record?.audit_record_id}
      <p class="audit-line">
        Audit: <code>{result.audit_record.audit_record_id}</code>
      </p>
    {/if}
  </article>
{/if}

<style>
  .model-result-card {
    background: var(--bg0);
    border: 1px solid var(--line-strong);
    border-left: 3px solid var(--info);
    padding: 0.65rem 0.7rem;
    display: grid;
    gap: 0.45rem;
  }

  .result-header {
    display: flex;
    flex-wrap: wrap;
    justify-content: space-between;
    align-items: flex-start;
    gap: 0.4rem;
  }

  .result-title-row {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.35rem;
  }

  .agent-tag {
    font-family: var(--mono);
    font-size: 0.58rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--ink-dim);
    border: 1px solid var(--line-strong);
    padding: 0.08rem 0.3rem;
  }

  .beat-id {
    font-size: 0.58rem;
    color: var(--ink-faint);
  }

  .status-pill {
    font-family: var(--mono);
    font-size: 0.58rem;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    padding: 0.08rem 0.35rem;
    border: 1px solid;
  }

  .status-pill[data-tone='ok'] {
    color: var(--ok);
    border-color: var(--ok);
  }

  .status-pill[data-tone='warn'] {
    color: var(--warn);
    border-color: var(--warn);
  }

  .status-pill[data-tone='alert'] {
    color: var(--alert);
    border-color: var(--alert);
  }

  .status-pill[data-tone='info'] {
    color: var(--info);
    border-color: var(--info);
  }

  .provenance-badge {
    font-family: var(--mono);
    font-size: 0.54rem;
    color: var(--ink-faint);
    border: 1px dashed var(--line-strong);
    padding: 0.1rem 0.35rem;
    letter-spacing: 0.03em;
  }

  .boundary-line {
    margin: 0;
    font-size: 0.62rem;
    font-family: var(--mono);
    color: var(--ok);
    letter-spacing: 0.02em;
  }

  .boundary-line.executed {
    color: var(--alert);
  }

  .problem {
    margin: 0;
    font-size: 0.68rem;
    color: var(--alert);
  }

  .detail-block,
  .audit-line,
  .confidence,
  .id-line,
  .rec-text {
    margin: 0;
    font-size: 0.66rem;
    color: var(--ink-dim);
    line-height: 1.4;
  }

  .payload-block {
    background: var(--bg2);
    border-left: 2px solid var(--assess);
    padding: 0.45rem 0.5rem;
    display: grid;
    gap: 0.3rem;
  }

  .payload-block h4 {
    margin: 0;
    font-size: 0.62rem;
    font-family: var(--mono);
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--ink-faint);
  }

  .payload-block ul {
    margin: 0;
    padding-left: 1rem;
    font-size: 0.68rem;
    color: var(--ink);
  }

  .quarantine-reason {
    margin: 0;
    font-size: 0.68rem;
    color: var(--warn);
    background: rgba(228, 196, 84, 0.08);
    border: 1px solid rgba(228, 196, 84, 0.35);
    padding: 0.4rem 0.45rem;
    line-height: 1.4;
  }

  .rec-block {
    border-left-color: var(--amber);
  }

  .luna-block {
    border-left-color: var(--info);
  }
</style>
