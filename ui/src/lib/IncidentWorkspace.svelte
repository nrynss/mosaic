<script>
  import RecurrenceSurface from './RecurrenceSurface.svelte';
  import ModelModeIndicator from './ModelModeIndicator.svelte';

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
    auditTargetID = $bindable(),
    auditTargetKind = $bindable(),
    onPrefillMaintenance
  } = $props();

  let activeIncident = $derived(arrayOf(cop?.cop?.incidents || cop?.incidents)[0]);
  let claimItems = $derived(makeClaimItems(cop?.cop || cop));
  let hasCurrentInsight = $derived(arrayOf(advisories?.insights).some(ins => ins.status === 'current'));
  let hasCurrentRecommendation = $derived(arrayOf(advisories?.recommendations).some(rec => rec.status === 'current'));

  function arrayOf(value) {
    return Array.isArray(value) ? value : [];
  }

  function claimLabel(claimClass) {
    if (claimClass === 'derived_assessment') return 'Derived assessment';
    if (claimClass === 'supervisor_recommendation') return 'Human-review recommendation';
    return 'Reported fact';
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
        title: `${weather.status || 'status unknown'} · ${weather.severity || 'unspecified'} alert`,
        detail: weather.summary || 'Weather alert status',
        timestamp: weather.updated_at,
        evidence: { kind: 'canonical_event', id: weather.source_event_id }
      });
    }
    return items.sort((left, right) => String(right.timestamp || '').localeCompare(String(left.timestamp || '')));
  }
</script>

<div class="incident-workspace-container">
  <!-- Active Incident details bar at the top -->
  <div class="active-incident-banner">
    <div class="incident-meta-grid">
      <div class="meta-item">
        <span class="meta-label">DISPATCH ID</span>
        <span class="meta-val"><code>{activeIncident?.incident_id || '—'}</code></span>
      </div>
      <div class="meta-item">
        <span class="meta-label">LOCATION</span>
        <span class="meta-val">{activeIncident?.location_id || '—'}</span>
      </div>
      <div class="meta-item">
        <span class="meta-label">CATEGORY</span>
        <span class="meta-val">{activeIncident?.category || '—'}</span>
      </div>
      <div class="meta-item">
        <span class="meta-label">ELAPSED TIME</span>
        <span class="meta-val elapsed-val">{formatTime(elapsedSeconds)}</span>
      </div>
    </div>
    <div class="analyze-action">
      <ModelModeIndicator providers={advisories?.providers} />
      <button class="analyze-button" onclick={loadAdvisories} disabled={advisoriesState === 'loading'}>
        {#if advisoriesState === 'loading'}
          Analyzing...
        {:else}
          Analyze Incident
        {/if}
      </button>
    </div>
  </div>

  <RecurrenceSurface {cop} {advisories} {onPrefillMaintenance} />

  <!-- Main Ledger content split layout (Facts vs Advisories) -->
  <div class="workspace-grid">
    <section class="ledger-column facts-column" aria-labelledby="cop-title">
      <div class="column-header">
        <div>
          <p class="eyebrow">Current common operating picture</p>
          <h2 id="cop-title">State revision <span>{cop?.state_revision ?? cop?.cop?.state_revision ?? '—'}</span></h2>
        </div>
        <div class="revision-meta">
          <span>Projected</span>
          <strong>{formatTimestamp(cop?.projected_at || cop?.cop?.projected_at)}</strong>
        </div>
      </div>

      <div class="claim-key" aria-label="Claim class key">
        <span class="key-item reported"><i></i>Reported fact</span>
        <span class="key-item assessed"><i></i>Derived assessment</span>
        <span class="key-item recommended"><i></i>Human-review recommendation</span>
      </div>

      {#if copState === 'loading' || copState === 'idle'}
        <div class="empty-state" aria-live="polite">Loading the deterministic COP…</div>
      {:else if copState === 'error'}
        <div class="empty-state error-state" role="alert">
          <strong>COP unavailable</strong>
          <p>{copError}</p>
        </div>
      {:else if claimItems.length === 0}
        <div class="empty-state">
          <strong>No source-derived facts are in this COP yet.</strong>
          <p>When the simulator or a valid source appends canonical events, this ledger will show the resulting state and its evidence.</p>
        </div>
      {:else}
        <ol class="claim-ledger" aria-label="Current source-derived state">
          {#each claimItems as item (item.kind + item.id)}
            <li class="claim-item {item.class}">
              <span class="ledger-pin" aria-hidden="true"></span>
              <div class="claim-time">{formatTimestamp(item.timestamp)}</div>
              <article>
                <div class="claim-topline">
                  <p class="claim-class">{claimLabel(item.class)}</p>
                  <span class="entity-kind">{item.kind}</span>
                </div>
                <h3>{item.title}</h3>
                <p>{item.detail}</p>
                <div class="claim-footer">
                  <code>{item.id}</code>
                  {#if item.evidence.id}
                    <button class="evidence-button" onclick={() => selectEvidence(item.evidence.kind, item.evidence.id, `${item.kind} · ${item.id}`)}>
                      Resolve evidence
                    </button>
                  {:else}
                    <span class="missing-evidence">Evidence ID unavailable</span>
                  {/if}
                </div>
              </article>
            </li>
          {/each}
        </ol>
      {/if}
    </section>

    <!-- Advisories section -->
    <section class="ledger-column advisories-column">
      {#if advisoriesState === 'loading'}
        <div class="empty-state" aria-live="polite">Loading advisories…</div>
      {:else if advisoriesState === 'unavailable'}
        <div class="advisory-composition" data-state="unavailable">
          <div class="advisory-column">
            <p class="claim-class assessed">Derived assessment</p>
            <div class="empty-advisory-state" data-state="unavailable">
              <h3>Assessment unavailable</h3>
              <p>Live Terra model transport is not composed, or structured fixture response was refused, invalid, or failed.</p>
            </div>
          </div>
          <div class="advisory-column">
            <p class="claim-class recommended">Human-review recommendation</p>
            <div class="empty-advisory-state" data-state="unavailable">
              <h3>Recommendation unavailable</h3>
              <p>Live Sol model transport is not composed, or structured fixture response was refused, invalid, or failed.</p>
            </div>
          </div>
        </div>
      {:else if advisoriesState === 'empty'}
        <div class="advisory-composition" data-state="empty">
          <div class="advisory-column">
            <p class="claim-class assessed">Derived assessment</p>
            <div class="empty-advisory-state" data-state="empty">
              <h3>No assessment composed</h3>
              <p>No local fixture is composed, or the advisory history is empty.</p>
            </div>
          </div>
          <div class="advisory-column">
            <p class="claim-class recommended">Human-review recommendation</p>
            <div class="empty-advisory-state" data-state="empty">
              <h3>No recommendation composed</h3>
              <p>No local fixture is composed, or the advisory history is empty.</p>
            </div>
          </div>
        </div>
      {:else if advisoriesState === 'ready' && advisories}
        <div class="advisories-header">
          <p class="advisory-mode-badge" data-mode={advisories.status || 'unavailable'}>
            Composition Mode: {(advisories.status || 'unavailable').replaceAll('_', ' ').replaceAll('-', ' ')}
          </p>
        </div>

        <div class="advisory-composition" data-state="ready">
          <div class="advisory-column">
            <p class="claim-class assessed">Derived assessment</p>
            {#if hasCurrentInsight}
              <!-- Current assessment rendered below -->
            {:else}
              <div class="empty-advisory-state" data-state="superseded">
                <h3>No current assessment is active.</h3>
                <p>The final road-opening correction has superseded the previous assessment.</p>
              </div>
            {/if}

            {#each arrayOf(advisories.insights) as ins (ins.insight_id)}
              <div class="advisory-card assessed-card" data-status={ins.status}>
                <div class="card-header">
                  <strong>{ins.insight_id}</strong>
                  <span class="status-badge" data-status={ins.status}>{ins.status.replaceAll('_', ' ')}</span>
                </div>
                <div class="card-body">
                  <h4>Assertions</h4>
                  <ul>
                    {#each arrayOf(ins.assertions) as assertion}
                      <li>{assertion}</li>
                    {/each}
                  </ul>
                  {#if ins.confidence}
                    <div class="confidence-info">
                      <strong>Confidence</strong>: {ins.confidence.basis} (Source: {ins.confidence.source_quality}, Reasoning: {ins.confidence.reasoning_support})
                    </div>
                  {/if}
                </div>
                <div class="card-footer">
                  <button class="evidence-button" onclick={() => selectEvidence('insight', ins.insight_id, `Insight · ${ins.insight_id}`)}>
                    Resolve evidence
                  </button>
                  <button class="prefill-button" onclick={() => { auditTargetID = ins.insight_id; auditTargetKind = 'insight'; }}>
                    Review insight
                  </button>
                </div>
              </div>
            {/each}
          </div>

          <div class="advisory-column">
            <p class="claim-class recommended">Human-review recommendation</p>
            {#if hasCurrentRecommendation}
              <!-- Current recommendation rendered below -->
            {:else}
              <div class="empty-advisory-state" data-state="not-current">
                <h3>No current recommendation is active.</h3>
                <p>No current operational recommendation is active at this revision.</p>
              </div>
            {/if}

            {#each arrayOf(advisories.recommendations) as rec (rec.recommendation_id)}
              <div class="advisory-card recommended-card" data-status={rec.status}>
                <div class="card-header">
                  <strong>{rec.recommendation_id}</strong>
                  <span class="status-badge" data-status={rec.status}>{rec.status.replaceAll('_', ' ')}</span>
                </div>
                <div class="card-body">
                  <p>{rec.text}</p>
                </div>
                <div class="card-footer">
                  <button class="evidence-button" onclick={() => selectEvidence('recommendation', rec.recommendation_id, `Recommendation · ${rec.recommendation_id}`)}>
                    Resolve evidence
                  </button>
                  <button class="prefill-button" onclick={() => { auditTargetID = rec.recommendation_id; auditTargetKind = 'recommendation'; }}>
                    Review recommendation
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
