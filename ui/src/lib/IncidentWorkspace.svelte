<script>
  import RecurrenceSurface from './RecurrenceSurface.svelte';
  import ModelModeIndicator from './ModelModeIndicator.svelte';
  import ModelActions from './ModelActions.svelte';
  import HelpTip from './HelpTip.svelte';

  let {
    cop,
    copState,
    copError,
    advisories,
    advisoriesState,
    advisoriesError,
    elapsedSeconds,
    loadAdvisories,
    selectEvidence,
    modelUsage = null,
    readEnvelope = null,
    auditTargetID = $bindable(),
    auditTargetKind = $bindable(),
    onPrefillMaintenance
  } = $props();

  let activeIncident = $derived(arrayOf(cop?.cop?.incidents || cop?.incidents)[0]);
  let claimItems = $derived(makeClaimItems(cop?.cop || cop));
  let hasCurrentInsight = $derived(arrayOf(advisories?.insights).some(ins => ins.status === 'current'));
  let hasCurrentRecommendation = $derived(arrayOf(advisories?.recommendations).some(rec => rec.status === 'current'));

  // Superseded / not-current advice stays visible by default so the
  // supersede moment reads on the board; operators can collapse it.
  let showHistory = $state(true);
  let visibleInsights = $derived(arrayOf(advisories?.insights).filter((ins) => showHistory || ins.status === 'current'));
  let visibleRecommendations = $derived(arrayOf(advisories?.recommendations).filter((rec) => showHistory || rec.status === 'current'));

  function arrayOf(value) {
    return Array.isArray(value) ? value : [];
  }

  function claimLabel(claimClass) {
    if (claimClass === 'derived_assessment') return 'Suggested assessment';
    if (claimClass === 'supervisor_recommendation') return 'Suggestion for you to review';
    return 'Fact from the scenario';
  }

  function formatTimestamp(value) {
    if (!value) return 'Time not recorded';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return new Intl.DateTimeFormat(undefined, {
      day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit', timeZoneName: 'short'
    }).format(date);
  }

  function formatTime(total) {
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const remainder = total % 60;
    if (hours > 0) return `${hours}h ${minutes}m ${remainder}s`;
    if (minutes > 0) return `${minutes}m ${remainder}s`;
    return `${remainder}s`;
  }

  function makeClaimItems(snapshot) {
    if (!snapshot) {
      return [];
    }
    const items = [];
    for (const incident of arrayOf(snapshot.incidents)) {
      items.push({
        class: 'reported_fact',
        kind: 'Incident',
        id: incident.incident_id,
        status: String(incident.status || ''),
        title: `${incident.category || 'Incident'} · ${incident.status || 'status unknown'}`,
        detail: `Location ${incident.location_id || 'not recorded'}`,
        timestamp: incident.opened_at,
        evidence: { kind: 'state_fact', id: incident.incident_id }
      });
    }
    for (const unit of arrayOf(snapshot.units)) {
      items.push({
        class: 'reported_fact',
        kind: 'Unit',
        id: unit.unit_id,
        status: String(unit.availability || ''),
        title: `${unit.availability || 'status unknown'} · ${unit.unit_id}`,
        detail: unit.incident_id ? `Linked to ${unit.incident_id}` : 'No incident link recorded',
        timestamp: unit.updated_at,
        evidence: { kind: 'canonical_event', id: unit.source_event_id }
      });
    }
    for (const resource of arrayOf(snapshot.resources)) {
      items.push({
        class: 'reported_fact',
        kind: 'Resource',
        id: resource.resource_id,
        status: String(resource.availability || ''),
        title: `${resource.availability || 'status unknown'} · ${resource.resource_id}`,
        detail: resource.incident_id ? `Linked to ${resource.incident_id}` : 'No incident link recorded',
        timestamp: resource.updated_at,
        evidence: { kind: 'canonical_event', id: resource.source_event_id }
      });
    }
    for (const road of arrayOf(snapshot.roads)) {
      items.push({
        class: 'reported_fact',
        kind: 'Road',
        id: road.road_id,
        status: String(road.status || ''),
        title: `${road.status || 'status unknown'} · ${road.name || road.road_id}`,
        detail: 'Current effective road condition',
        timestamp: road.updated_at,
        evidence: { kind: 'canonical_event', id: road.effective_event_id }
      });
    }
    for (const weather of arrayOf(snapshot.weather_alerts)) {
      items.push({
        class: 'reported_fact',
        kind: 'Weather',
        id: weather.weather_alert_id,
        status: String(weather.status || ''),
        title: `${weather.status || 'status unknown'} · ${weather.severity || 'unspecified'} alert`,
        detail: weather.summary || 'Weather alert status',
        timestamp: weather.updated_at,
        evidence: { kind: 'canonical_event', id: weather.source_event_id }
      });
    }
    return items.sort((left, right) => String(right.timestamp || '').localeCompare(String(left.timestamp || '')));
  }
</script>

<div class="incident-workspace-container" data-testid="incident-workspace">
  <!-- Active Incident details bar at the top -->
  <div class="active-incident-banner" data-testid="active-incident-banner">
    <div class="incident-meta-grid">
      <div class="meta-item">
        <span class="meta-label">Call / incident</span>
        <span class="meta-val" data-testid="active-incident-id"><code>{activeIncident?.incident_id || 'Not on board yet'}</code></span>
      </div>
      <div class="meta-item">
        <span class="meta-label">Where</span>
        <span class="meta-val">{activeIncident?.location_id || '—'}</span>
      </div>
      <div class="meta-item">
        <span class="meta-label">What kind of call</span>
        <span class="meta-val">{activeIncident?.category || '—'}</span>
      </div>
      <div class="meta-item">
        <span class="meta-label">Demo clock</span>
        <span class="meta-val elapsed-val" data-testid="workspace-demo-clock">{formatTime(elapsedSeconds)}</span>
      </div>
    </div>
    <div class="analyze-action">
      <ModelModeIndicator providers={advisories?.providers} {modelUsage} />
      <button
        class="analyze-button"
        data-testid="refresh-advice"
        data-state={advisoriesState}
        onclick={loadAdvisories}
        disabled={advisoriesState === 'loading'}
      >
        {#if advisoriesState === 'loading'}
          Refreshing advice…
        {:else}
          Refresh advice
        {/if}
        <HelpTip text="Re-polls current assessments and recommendations for this synthetic call. When agents are live, operator analyze uses the model; the board itself only changes from the scenario pipeline." label="About Refresh advice" />
      </button>
    </div>
  </div>

  <RecurrenceSurface {cop} {advisories} {onPrefillMaintenance} />

  {#if readEnvelope}
    <ModelActions
      {readEnvelope}
      {loadAdvisories}
      cassetteModeHint={advisories?.cassette_mode || ''}
      copRevision={cop?.state_revision ?? cop?.cop?.state_revision ?? null}
    />
  {/if}

  <!-- Main Ledger content split layout (Facts vs Advisories) -->
  <div class="workspace-grid">
    <section class="ledger-column facts-column" data-testid="cop-board" aria-labelledby="cop-title">
      <div class="column-header">
        <div>
          <p class="eyebrow">
            What we know right now
            <HelpTip text="This is the trusted board for the synthetic incident. It is built only from scenario events — AI suggestions cannot rewrite it." label="About the incident board" />
          </p>
          <h2 id="cop-title">
            Picture update
            <span
              data-testid="cop-revision"
              data-revision={String(cop?.state_revision ?? cop?.cop?.state_revision ?? '')}
            >#{cop?.state_revision ?? cop?.cop?.state_revision ?? '—'}</span>
          </h2>
        </div>
        <div class="revision-meta">
          <span>Last built</span>
          <strong>{formatTimestamp(cop?.projected_at || cop?.cop?.projected_at)}</strong>
        </div>
      </div>

      <div class="claim-key" data-testid="cop-claim-key" aria-label="How to read the board">
        <span class="key-item reported" data-testid="claim-key-reported"><i></i>Fact from the scenario</span>
        <span class="key-item assessed" data-testid="claim-key-assessed"><i></i>Suggested assessment</span>
        <span class="key-item recommended" data-testid="claim-key-recommended"><i></i>Suggestion for you</span>
      </div>

      {#if copState === 'loading' || copState === 'idle'}
        <div class="empty-state" data-testid="cop-empty" data-state="loading" aria-live="polite">Loading the incident board…</div>
      {:else if copState === 'error'}
        <div class="empty-state error-state" data-testid="cop-empty" data-state="error" role="alert">
          <strong>Incident board unavailable</strong>
          <p>{copError}</p>
        </div>
      {:else if claimItems.length === 0}
        <div class="empty-state" data-testid="cop-empty" data-state="empty">
          <strong>No facts on the board yet.</strong>
          <p>
            Press <strong>Play scenario</strong> above to run the synthetic domestic-disturbance call.
            The board fills as each step arrives (intake, weather, roads, unit, EMS).
          </p>
        </div>
      {:else}
        <ol class="claim-ledger" data-testid="cop-claim-ledger" aria-label="Current incident facts">
          {#each claimItems as item (item.kind + item.id)}
            <li
              class="claim-item {item.class}"
              data-testid="cop-claim-row"
              data-kind={item.kind}
              data-entity-id={item.id}
              data-claim-class={item.class}
              data-status={item.status || ''}
            >
              <span class="ledger-pin" aria-hidden="true"></span>
              <div class="claim-time">{formatTimestamp(item.timestamp)}</div>
              <article>
                <div class="claim-topline">
                  <p class="claim-class" data-testid="cop-claim-class" data-claim-class={item.class}>{claimLabel(item.class)}</p>
                  <span class="entity-kind" data-testid="cop-entity-kind">{item.kind}</span>
                </div>
                <h3 data-testid="cop-claim-title">{item.title}</h3>
                <p data-testid="cop-claim-detail">{item.detail}</p>
                <div class="claim-footer">
                  <code data-testid="cop-claim-id">{item.id}</code>
                  {#if item.evidence.id}
                    <button
                      class="evidence-button"
                      data-testid="cop-show-source"
                      onclick={() => selectEvidence(item.evidence.kind, item.evidence.id, `${item.kind} · ${item.id}`)}
                    >
                      Show source
                    </button>
                  {:else}
                    <span class="missing-evidence">No source linked</span>
                  {/if}
                </div>
              </article>
            </li>
          {/each}
        </ol>
      {/if}
    </section>

    <!-- Advisories section -->
    <section class="ledger-column advisories-column" data-testid="advisories-panel" data-state={advisoriesState}>
      {#if advisoriesState === 'loading'}
        <div class="empty-state" data-testid="advisories-empty" data-state="loading" aria-live="polite">Loading advice…</div>
      {:else if advisoriesState === 'unavailable'}
        <div class="advisory-composition" data-testid="advisory-composition" data-state="unavailable">
          <div class="advisory-column">
            <p class="claim-class assessed">Suggested assessment</p>
            <div class="empty-advisory-state" data-state="unavailable">
              <h3>Assessment not available</h3>
              <p>The AI could not answer this time. The incident board is unchanged.</p>
            </div>
          </div>
          <div class="advisory-column">
            <p class="claim-class recommended">Suggestion for you</p>
            <div class="empty-advisory-state" data-state="unavailable">
              <h3>Recommendation not available</h3>
              <p>Sol could not produce a recommendation. Nothing was sent to any team.</p>
            </div>
          </div>
        </div>
      {:else if advisoriesState === 'empty'}
        <div class="advisory-composition" data-testid="advisory-composition" data-state="empty">
          <div class="advisory-column">
            <p class="claim-class assessed">Suggested assessment</p>
            <div class="empty-advisory-state" data-state="empty">
              <h3>No assessment yet</h3>
              <p>Play the scenario, then press <strong>Refresh advice</strong>.</p>
            </div>
          </div>
          <div class="advisory-column">
            <p class="claim-class recommended">Suggestion for you</p>
            <div class="empty-advisory-state" data-state="empty">
              <h3>No recommendation yet</h3>
              <p>Recommendations appear after the scenario’s analysis pack loads.</p>
            </div>
          </div>
        </div>
      {:else if advisoriesState === 'ready' && advisories}
        <div class="advisories-header">
          <button
            type="button"
            class="history-toggle"
            data-testid="advice-history-toggle"
            data-show-history={showHistory ? 'true' : 'false'}
            aria-pressed={showHistory}
            onclick={() => (showHistory = !showHistory)}
          >
            {showHistory ? 'Hide past advice' : 'Show past advice'}
          </button>
          <p
            class="advisory-mode-badge"
            data-testid="advice-source-badge"
            data-mode={advisories.status || 'unavailable'}
          >
            Advice source: {advisories.status && advisories.status.includes('live') ? 'Live AI' : 'Demo pack'}
          </p>
        </div>

        <div class="advisory-composition" data-testid="advisory-composition" data-state="ready">
          <div class="advisory-column" data-testid="advice-insights-column">
            <p class="claim-class assessed" data-testid="claim-class-assessed">Suggested assessment</p>
            {#if hasCurrentInsight}
              <!-- Current assessment rendered below -->
            {:else}
              <div class="empty-advisory-state" data-testid="advice-insight-empty" data-state="superseded">
                <h3>Earlier assessment is now out of date</h3>
                <p>The road-opening correction made the earlier access warning out of date — that is part of the demo story.</p>
              </div>
            {/if}

            {#each visibleInsights as ins (ins.insight_id)}
              <div
                class="advisory-card assessed-card"
                data-testid="advice-insight-card"
                data-insight-id={ins.insight_id}
                data-status={ins.status}
              >
                <div class="card-header">
                  <strong data-testid="advice-insight-id">{ins.insight_id}</strong>
                  <span class="status-badge" data-testid="advice-insight-status" data-status={ins.status}>{ins.status.replaceAll('_', ' ')}</span>
                </div>
                <div class="card-body">
                  <ul data-testid="advice-insight-assertions">
                    {#each arrayOf(ins.assertions) as assertion}
                      <li>{assertion}</li>
                    {/each}
                  </ul>
                  {#if ins.confidence}
                    <div class="confidence-info">
                      <strong>Why trust it?</strong> {ins.confidence.basis} (sources: {ins.confidence.source_quality}, reasoning: {ins.confidence.reasoning_support})
                    </div>
                  {/if}
                </div>
                <div class="card-footer">
                  <button
                    class="evidence-button"
                    data-testid="advice-insight-evidence"
                    onclick={() => selectEvidence('insight', ins.insight_id, `Assessment · ${ins.insight_id}`)}
                  >
                    Show source
                  </button>
                  <button class="prefill-button" data-testid="advice-insight-use-decision" onclick={() => { auditTargetID = ins.insight_id; auditTargetKind = 'insight'; }}>
                    Use in my decision
                  </button>
                </div>
              </div>
            {/each}
          </div>

          <div class="advisory-column" data-testid="advice-recommendations-column">
            <p class="claim-class recommended" data-testid="claim-class-recommended">Suggestion for you</p>
            {#if hasCurrentRecommendation}
              <!-- Current recommendation rendered below -->
            {:else}
              <div class="empty-advisory-state" data-testid="advice-recommendation-empty" data-state="not-current">
                <h3>No still-current recommendation</h3>
                <p>Earlier advice may be marked not current after the road reopened. You can still open it for history.</p>
              </div>
            {/if}

            {#each visibleRecommendations as rec (rec.recommendation_id)}
              <div
                class="advisory-card recommended-card"
                data-testid="advice-recommendation-card"
                data-recommendation-id={rec.recommendation_id}
                data-status={rec.status}
              >
                <div class="card-header">
                  <strong data-testid="advice-recommendation-id">{rec.recommendation_id}</strong>
                  <span class="status-badge" data-testid="advice-recommendation-status" data-status={rec.status}>{rec.status.replaceAll('_', ' ')}</span>
                </div>
                <div class="card-body">
                  <p data-testid="advice-recommendation-text">{rec.text}</p>
                </div>
                <div class="card-footer">
                  <button
                    class="evidence-button"
                    data-testid="advice-recommendation-evidence"
                    onclick={() => selectEvidence('recommendation', rec.recommendation_id, `Recommendation · ${rec.recommendation_id}`)}
                  >
                    Show source
                  </button>
                  <button class="prefill-button" data-testid="advice-recommendation-use-decision" onclick={() => { auditTargetID = rec.recommendation_id; auditTargetKind = 'recommendation'; }}>
                    Use in my decision
                  </button>
                </div>
              </div>
            {/each}
          </div>
        </div>
      {/if}
    </section>
  </div>
</div>
