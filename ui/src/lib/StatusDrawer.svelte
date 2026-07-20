<script>
  let {
    health,
    version,
    streamState,
    streamDetail,
    operations,
    operationsState,
    operationsError,
    operationsPresentation,
    apiBaseInput = $bindable(),
    applyAPIBase,
    loadOperations
  } = $props();

  let collapsed = $state(true);

  function toggle(event) {
    // Only toggle if clicking the header itself or the title, not the buttons/inputs inside
    if (event.target.closest('button') || event.target.closest('input')) {
      return;
    }
    collapsed = !collapsed;
  }

  function formatTimestamp(value) {
    if (!value) return 'Time not recorded';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return new Intl.DateTimeFormat(undefined, {
      day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit', timeZoneName: 'short'
    }).format(date);
  }

  function formatNumber(value) {
    const number = Number(value);
    return Number.isFinite(number) ? new Intl.NumberFormat().format(number) : '—';
  }

  function formatUptime(seconds) {
    const total = Math.max(0, Math.floor(Number(seconds) || 0));
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const remainder = total % 60;
    if (hours > 0) return `${hours}h ${minutes}m`;
    if (minutes > 0) return `${minutes}m ${remainder}s`;
    return `${remainder}s`;
  }

  function outcomeSummary(statuses) {
    const values = statuses || {};
    return [
      ['valid', values.valid],
      ['invalid', values.invalid],
      ['refused', values.refused],
      ['failed', values.failed],
      ['timed out', values.timed_out]
    ].map(([label, count]) => `${label} ${formatNumber(count || 0)}`).join(' · ');
  }

  function arrayOf(value) {
    return Array.isArray(value) ? value : [];
  }

  let operationsCapabilities = $derived(arrayOf(operations?.capabilities));
  let modelRunAgents = $derived(arrayOf(operations?.counts?.model_runs?.by_agent));
</script>

<div class="developer-status-drawer" class:collapsed={collapsed}>
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="drawer-header" onclick={toggle}>
    <div class="drawer-title">
      <span class="terminal-icon" aria-hidden="true">&gt;_</span>
      <strong>Developer Console & Status</strong>
    </div>
    
    <div class="compact-status-pills">
      <span class="pill-status" data-state={health?.state}>Health: {health?.detail || '—'}</span>
      <span class="pill-status" data-state={streamState}>Stream: {streamState === 'live' ? 'Connected' : streamState}</span>
      <span class="pill-status" data-state={operationsPresentation?.state}>Ops: {operationsPresentation?.label || '—'}</span>
    </div>

    <button class="toggle-drawer-button" onclick={() => collapsed = !collapsed}>
      {#if collapsed}
        Expand ▲
      {:else}
        Collapse ▼
      {/if}
    </button>
  </div>

  {#if !collapsed}
    <div class="drawer-content">
      <div class="drawer-grid">
        <!-- Connection and configuration -->
        <section class="drawer-section connection-config">
          <h3>Connection Settings</h3>
          <div class="form-group">
            <label for="drawer-api-base">API Base URL</label>
            <div class="input-action-group">
              <input id="drawer-api-base" bind:value={apiBaseInput} spellcheck="false" autocomplete="off" />
              <button class="action-btn" onclick={applyAPIBase}>Apply</button>
            </div>
          </div>
          <dl class="status-list">
            <div><dt>Health</dt><dd data-state={health?.state}>{health?.detail || '—'}</dd></div>
            <div><dt>Version</dt><dd data-state={version?.state}>{version?.detail || '—'}</dd></div>
            <div><dt>Stream</dt><dd data-state={streamState}>{streamDetail || '—'}</dd></div>
          </dl>
        </section>

        <!-- Operations summary -->
        <section class="drawer-section operations-summary">
          <h3>Operations Receipt</h3>
          {#if operationsState === 'loading' || operationsState === 'idle'}
            <div class="drawer-loading">Reading bounded operational records...</div>
          {:else if operationsState === 'unavailable'}
            <div class="drawer-error" role="alert">
              <p>Operations view unavailable: {operationsError || operationsPresentation?.detail}</p>
              <button class="quiet-button" onclick={() => loadOperations()}>Try Again</button>
            </div>
          {:else}
            <dl class="status-list scrollable-list">
              <div><dt>Observed</dt><dd>{formatTimestamp(operations?.observed_at)}</dd></div>
              <div><dt>Source Receipt</dt><dd>{formatTimestamp(operations?.latest_source_received_at)}</dd></div>
              <div><dt>COP Recovery</dt><dd>{operations?.recovery?.status || 'Not recorded'} · Rev {formatNumber(operations?.recovery?.state_revision)}</dd></div>
              <div><dt>Projected</dt><dd>{formatTimestamp(operations?.recovery?.projected_at)}</dd></div>
              <div><dt>Service Uptime</dt><dd>{formatUptime(operations?.service?.uptime_seconds)}</dd></div>
              <div><dt>Local Stream</dt><dd>{formatNumber(operations?.stream?.local_subscriber_count)} subscribers</dd></div>
            </dl>
          {/if}
        </section>

        <!-- Ledger counts -->
        <section class="drawer-section ledger-tally">
          <h3>Durable Ledger</h3>
          <dl class="counts-list">
            <div><dt>Raw Events</dt><dd>{formatNumber(operations?.counts?.raw_events)}</dd></div>
            <div><dt>Canonical</dt><dd>{formatNumber(operations?.counts?.canonical_events)}</dd></div>
            <div><dt>Projected</dt><dd>{formatNumber(operations?.counts?.projected_events)}</dd></div>
            <div><dt>Unprojected</dt><dd>{formatNumber(operations?.counts?.unprojected_events)}</dd></div>
            <div><dt>Checkpoints</dt><dd>{formatNumber(operations?.counts?.checkpoints)}</dd></div>
            <div><dt>Insights</dt><dd>{formatNumber(operations?.counts?.insights)}</dd></div>
            <div><dt>Recommendations</dt><dd>{formatNumber(operations?.counts?.recommendations)}</dd></div>
            <div><dt>Audit Records</dt><dd>{formatNumber(operations?.counts?.audit_records)}</dd></div>
          </dl>
        </section>

        <!-- Luna & Model runs -->
        <section class="drawer-section model-luna-status">
          <h3>Luna & Model Runs</h3>
          <div class="luna-outcomes">
            <h4>Luna Lifecycle</h4>
            <div class="luna-grid">
              <div><span>Accepted:</span> <strong>{formatNumber(operations?.counts?.luna_lifecycle?.accepted)}</strong></div>
              <div><span>Repaired:</span> <strong>{formatNumber(operations?.counts?.luna_lifecycle?.repaired)}</strong></div>
              <div><span>Quarantined:</span> <strong>{formatNumber(operations?.counts?.luna_lifecycle?.quarantined)}</strong></div>
              <div><span>Rejected:</span> <strong>{formatNumber(operations?.counts?.luna_lifecycle?.rejected)}</strong></div>
            </div>
          </div>
          <div class="model-runs-info">
            <h4>Model Runs (Total: {formatNumber(operations?.counts?.model_runs?.total)})</h4>
            <div class="runs-list">
              {#each modelRunAgents as agent (agent.agent)}
                <div class="agent-run-row">
                  <span class="agent-name">{agent.agent}</span>
                  <span class="agent-stats">
                    <strong>{formatNumber(agent.total)} runs</strong> ({outcomeSummary(agent.validation_statuses)})
                  </span>
                </div>
              {/each}
            </div>
          </div>
        </section>
      </div>

      <!-- Capability docket -->
      <section class="drawer-section capability-docket">
        <h3>Capability Docket</h3>
        <ul class="capability-cards-grid">
          {#each operationsCapabilities as capability (capability.capability)}
            <li class="cap-card" data-mode={capability.mode} data-status={capability.status}>
              <div class="cap-card-header">
                <code>{capability.capability.replaceAll('_', ' ')}</code>
                <span class="mode-tag">{capability.mode.replaceAll('_', ' ')}</span>
              </div>
              <h5>{capability.feature}</h5>
              <p>{capability.detail}</p>
              <strong class="status-tag">{capability.status}</strong>
            </li>
          {/each}
        </ul>
      </section>
    </div>
  {/if}
</div>
