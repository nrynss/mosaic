<script>
  import { onMount } from 'svelte';

  const defaultAPIBase = import.meta.env.VITE_MOSAIC_API_BASE_URL || '/api/v1';
  const hiddenArtifactFields = new Set(['payload_bytes_b64', 'raw_sha256']);

  let apiBase = $state(normaliseAPIBase(defaultAPIBase));
  let apiBaseInput = $state(normaliseAPIBase(defaultAPIBase));
  let health = $state({ state: 'idle', detail: 'Not checked' });
  let version = $state({ state: 'idle', detail: 'Not checked' });
  let cop = $state(null);
  let copState = $state('idle');
  let copError = $state('');
  let operations = $state(null);
  let operationsState = $state('idle');
  let operationsError = $state('');
  let advisories = $state(null);
  let advisoriesState = $state('idle');
  let advisoriesError = $state('');
  let streamState = $state('idle');
  let streamDetail = $state('Not connected');
  let selectedEvidence = $state(null);
  let evidenceState = $state('idle');
  let evidenceError = $state('');
  let briefingNote = $state('Request a structured supervisor briefing for the current synthetic COP.');
  let auditAction = $state('acknowledged');
  let auditTargetKind = $state('recommendation');
  let auditTargetID = $state('');
  let auditNote = $state('Public demo review recorded for the synthetic demonstration.');
  let actionState = $state('idle');
  let actionMessage = $state('');

  let streamController;
  let reconnectTimer;
  let reconnectAttempt = 0;
  let destroyed = false;

  let copStateRevision = $derived(cop?.state_revision ?? cop?.cop?.state_revision ?? '—');
  let claimItems = $derived(makeClaimItems(cop?.cop));
  let operationsPresentation = $derived(describeOperations(operations, operationsState));
  let operationsCapabilities = $derived(arrayOf(operations?.capabilities));
  let modelRunAgents = $derived(arrayOf(operations?.counts?.model_runs?.by_agent));
  let hasCurrentInsight = $derived(arrayOf(advisories?.insights).some(ins => ins.status === 'current'));
  let hasCurrentRecommendation = $derived(arrayOf(advisories?.recommendations).some(rec => rec.status === 'current'));

  onMount(() => {
    void refreshAll();
    return () => {
      destroyed = true;
      stopStream();
    };
  });

  function normaliseAPIBase(value) {
    const trimmed = String(value || '').trim().replace(/\/+$/, '');
    return trimmed || '/api/v1';
  }

  function apiURL(path) {
    const base = normaliseAPIBase(apiBase);
    const suffix = String(path || '').replace(/^\/+/, '');
    return `${base}/${suffix}`;
  }

  function headersFor(additional = {}) {
    return new Headers(additional);
  }

  async function readEnvelope(path, options = {}) {
    const response = await fetch(apiURL(path), {
      ...options,
      headers: headersFor(options.headers)
    });
    let body = {};
    try {
      body = await response.json();
    } catch {
      throw new Error(`The API returned ${response.status} without JSON.`);
    }
    if (!response.ok) {
      throw new Error(body?.error?.message || `The API returned ${response.status}.`);
    }
    return body.data;
  }

  async function refreshAll() {
    await Promise.all([loadPublicStatus(), loadCOP(), loadOperations(), loadAdvisories()]);
    connectStream();
  }

  async function loadPublicStatus() {
    health = { state: 'loading', detail: 'Checking health' };
    version = { state: 'loading', detail: 'Checking version' };
    const [healthResult, versionResult] = await Promise.allSettled([
      readEnvelope('health'),
      readEnvelope('version')
    ]);
    health = healthResult.status === 'fulfilled'
      ? { state: 'ready', detail: healthResult.value?.status || 'ok' }
      : { state: 'error', detail: healthResult.reason.message };
    version = versionResult.status === 'fulfilled'
      ? { state: 'ready', detail: versionResult.value?.version || 'Unknown version' }
      : { state: 'error', detail: versionResult.reason.message };
  }

  async function loadCOP() {
    copState = 'loading';
    copError = '';
    try {
      cop = await readEnvelope('cop');
      copState = 'ready';
    } catch (error) {
      cop = null;
      copState = 'error';
      copError = error.message;
    }
  }

  async function loadOperations({ quiet = false } = {}) {
    if (!quiet) {
      operationsState = 'loading';
    }
    operationsError = '';
    try {
      operations = await readEnvelope('operations');
      operationsState = Number(operations?.counts?.unprojected_events || 0) > 0 ? 'degraded' : 'ready';
    } catch (error) {
      operations = null;
      operationsState = 'unavailable';
      operationsError = error.message;
    }
  }

  async function loadAdvisories() {
    advisoriesState = 'loading';
    advisoriesError = '';
    try {
      advisories = await readEnvelope('advisories');
      if (advisories?.status === 'unavailable') {
        advisoriesState = 'unavailable';
      } else {
        const hasInsights = arrayOf(advisories?.insights).length > 0;
        const hasRecs = arrayOf(advisories?.recommendations).length > 0;
        if (!hasInsights && !hasRecs) {
          advisoriesState = 'empty';
        } else {
          advisoriesState = 'ready';
        }
      }
    } catch (error) {
      advisories = null;
      advisoriesState = 'unavailable';
      advisoriesError = error.message;
    }
  }

  function applyAPIBase() {
    apiBase = normaliseAPIBase(apiBaseInput);
    apiBaseInput = apiBase;
    selectedEvidence = null;
    evidenceState = 'idle';
    evidenceError = '';
    void refreshAll();
  }

  async function selectEvidence(kind, id, label) {
    evidenceState = 'loading';
    evidenceError = '';
    selectedEvidence = { kind, id, label, resolved: false };
    try {
      const result = await resolveEvidence(kind, id);
      selectedEvidence = { ...result, label };
      evidenceState = result.resolved ? 'ready' : 'unresolved';
    } catch (error) {
      evidenceState = 'error';
      evidenceError = error.message;
    }
  }

  async function resolveEvidence(kind, id) {
    const response = await fetch(apiURL(`evidence/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`), {
      headers: headersFor()
    });
    let body = {};
    try {
      body = await response.json();
    } catch {
      throw new Error(`The evidence API returned ${response.status} without JSON.`);
    }
    // A 404 from P08 is an explicit unresolved Evidence Resolution, not a
    // transport failure. Preserve it so the interface can name that state.
    if (response.status === 404 && body?.data) {
      return body.data;
    }
    if (!response.ok) {
      throw new Error(body?.error?.message || `The evidence API returned ${response.status}.`);
    }
    return body.data;
  }
  function stopStream() {
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = undefined;
    }
    if (streamController) {
      streamController.abort();
      streamController = undefined;
    }
  }

  async function connectStream() {
    stopStream();
    const controller = new AbortController();
    streamController = controller;
    streamState = 'connecting';
    streamDetail = 'Opening evidence stream';

    try {
      const response = await fetch(apiURL('stream'), {
        headers: headersFor(),
        signal: controller.signal
      });
      if (!response.ok || !response.body) {
        let detail = `The stream returned ${response.status}.`;
        try {
          const body = await response.json();
          detail = body?.error?.message || detail;
        } catch {
          // The status is the useful fallback when the stream did not produce JSON.
        }
        throw new Error(detail);
      }
      streamState = 'live';
      streamDetail = 'Live updates connected';
      reconnectAttempt = 0;
      void loadOperations({ quiet: true });
      await consumeSSE(response.body, controller.signal);
      if (!controller.signal.aborted) {
        throw new Error('The evidence stream ended.');
      }
    } catch (error) {
      if (controller.signal.aborted || destroyed || streamController !== controller) {
        return;
      }
      streamState = 'reconnecting';
      streamDetail = error.message;
      scheduleReconnect();
    }
  }

  function scheduleReconnect() {
    if (destroyed) {
      return;
    }
    const delay = Math.min(1000 * 2 ** reconnectAttempt, 12000);
    reconnectAttempt += 1;
    streamDetail = `${streamDetail} Retrying in ${Math.round(delay / 1000)}s.`;
    reconnectTimer = setTimeout(() => {
      reconnectTimer = undefined;
      void connectStream();
    }, delay);
  }

  async function consumeSSE(body, signal) {
    const reader = body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    let eventName = 'message';
    let dataLines = [];

    const dispatch = () => {
      if (!dataLines.length) {
        eventName = 'message';
        return;
      }
      const data = dataLines.join('\n');
      dataLines = [];
      let payload;
      try {
        payload = JSON.parse(data);
      } catch {
        eventName = 'message';
        return;
      }
      applyStreamEvent(eventName, payload);
      eventName = 'message';
    };

    try {
      while (!signal.aborted) {
        const { done, value } = await reader.read();
        if (done) {
          break;
        }
        buffer += decoder.decode(value, { stream: true });
        const lines = buffer.split(/\r?\n/);
        buffer = lines.pop() ?? '';
        for (const line of lines) {
          if (line === '') {
            dispatch();
          } else if (line.startsWith('event:')) {
            eventName = line.slice(6).trim() || 'message';
          } else if (line.startsWith('data:')) {
            dataLines.push(line.slice(5).trimStart());
          }
        }
      }
    } finally {
      reader.releaseLock();
    }
  }

  function applyStreamEvent(name, payload) {
    if (name === 'cop.snapshot' && payload?.cop) {
      cop = payload;
      copState = 'ready';
      copError = '';
      void loadAdvisories();
      return;
    }
    // P08's named event means the read model changed. Re-reading the COP keeps
    // the dashboard deterministic even when an event's payload is only a notice.
    void loadCOP();
    void loadOperations({ quiet: true });
    void loadAdvisories();
  }

  function submitBriefing(event) {
    event.preventDefault();
    void requestBriefing();
  }

  function submitAuditAction(event) {
    event.preventDefault();
    void recordAuditAction();
  }
  async function requestBriefing() {
    actionState = 'loading';
    actionMessage = '';
    try {
      const result = await readEnvelope('briefings', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ note: briefingNote })
      });
      actionState = 'ready';
      actionMessage = `Briefing request recorded (${result.briefing_id}); executed: ${String(result.executed)}.`;
    } catch (error) {
      actionState = 'error';
      actionMessage = error.message;
    }
  }

  async function recordAuditAction() {
    actionState = 'loading';
    actionMessage = '';
    try {
      const result = await readEnvelope('audit-actions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          action: auditAction,
          target_kind: auditTargetKind,
          target_id: auditTargetID,
          note: auditNote
        })
      });
      actionState = 'ready';
      actionMessage = `Review record created (${result.audit_record?.audit_record_id || 'audit record'}); executed: ${String(result.executed)}.`;
    } catch (error) {
      actionState = 'error';
      actionMessage = error.message;
    }
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

  function describeOperations(snapshot, state) {
    if (state === 'loading' || state === 'idle') {
      return { state: 'loading', label: 'Collecting', detail: 'Reading bounded operational records.' };
    }
    if (state === 'unavailable') {
      return { state: 'unavailable', label: 'Unavailable', detail: 'The bounded operations read model is not available.' };
    }
    const unprojected = Number(snapshot?.counts?.unprojected_events || 0);
    if (unprojected > 0) {
      return {
        state: 'degraded',
        label: 'Degraded',
        detail: `${formatNumber(unprojected)} canonical event${unprojected === 1 ? '' : 's'} have no projection receipt.`
      };
    }
    if (snapshot?.recovery?.status === 'recovered') {
      return { state: 'recovered', label: 'Recovered', detail: 'Deterministic recovery completed for this observation.' };
    }
    return { state: 'ready', label: 'Observed', detail: 'Bounded records are available for this observation.' };
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
  function safeArtifact(value) {
    if (Array.isArray(value)) return value.map(safeArtifact);
    if (value && typeof value === 'object') {
      return Object.fromEntries(
        Object.entries(value)
          .filter(([key]) => !hiddenArtifactFields.has(key))
          .map(([key, child]) => [key, safeArtifact(child)])
      );
    }
    return value;
  }

  function evidenceText() {
    if (!selectedEvidence?.artifact) return '';
    return JSON.stringify(safeArtifact(selectedEvidence.artifact), null, 2);
  }
</script>

<svelte:head>
  <title>Mosaic — Evidence ledger</title>
</svelte:head>

<header class="masthead">
  <a class="wordmark" href="#cop" aria-label="Mosaic evidence ledger home">
    <span class="wordmark-mark" aria-hidden="true"></span>
    <span>Mosaic</span>
    <small>evidence ledger</small>
  </a>
  <p class="scope">Synthetic demo · single instance · no external actions</p>
  <div class="connection-pill" data-state={streamState} aria-live="polite">
    <span aria-hidden="true"></span>
    {streamState === 'live' ? 'Live' : streamState === 'reconnecting' ? 'Reconnecting' : 'Checking'}
  </div>
</header>

<main class="folio">
  <aside class="context-rail" aria-label="Demo context">
    <section class="rail-section">
      <p class="eyebrow">Public demo</p>
      <h1>Read the record.<br />Keep judgment human.</h1>
      <p class="rail-copy">This synthetic demonstration is open to anyone. Review calls append an immutable record; they never dispatch an external action.</p>
    </section>

    <section class="rail-section connection-form">
      <p class="eyebrow">Connection</p>
      <label for="api-base">API base URL</label>
      <input id="api-base" bind:value={apiBaseInput} spellcheck="false" autocomplete="off" />
      <button class="quiet-button" onclick={applyAPIBase}>Apply endpoint</button>
      <dl class="service-status">
        <div><dt>Health</dt><dd data-state={health.state}>{health.detail}</dd></div>
        <div><dt>Version</dt><dd data-state={version.state}>{version.detail}</dd></div>
        <div><dt>Stream</dt><dd data-state={streamState}>{streamDetail}</dd></div>
        <div><dt>Operations</dt><dd data-state={operationsPresentation.state}>{operationsPresentation.label}</dd></div>
      </dl>
    </section>

    <section class="rail-section boundary-note">
      <p class="eyebrow">Boundary</p>
      <p>Claims describe the current deterministic record. Mosaic does not dispatch, contact, or alter an operational system.</p>
    </section>
  </aside>

  <section class="ledger" id="cop" aria-labelledby="cop-title">
    <div class="ledger-heading">
      <div>
        <p class="eyebrow">Current common operating picture</p>
        <h2 id="cop-title">State revision <span>{copStateRevision}</span></h2>
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
        <button class="quiet-button" onclick={refreshAll}>Try again</button>
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

    {#if advisoriesState === 'loading'}
      <div class="empty-state" aria-live="polite">Loading advisories…</div>
    {:else if advisoriesState === 'unavailable'}
      <section class="advisory-composition" data-state="unavailable" aria-label="Advisory composition is unavailable">
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
      </section>
    {:else if advisoriesState === 'empty'}
      <section class="advisory-composition" data-state="empty" aria-label="No advisories are composed">
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
      </section>
    {:else if advisoriesState === 'ready' && advisories}
      <div class="advisories-header">
        <p class="advisory-mode-badge" data-mode={advisories.status || 'unavailable'}>
          Composition Mode: {(advisories.status || 'unavailable').replaceAll('_', ' ').replaceAll('-', ' ')}
        </p>
      </div>

      <section class="advisory-composition" data-state="ready" aria-label="Advisory assessment and recommendations">
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
      </section>
    {/if}
    <section class="operations-receipt" aria-labelledby="operations-title" data-state={operationsPresentation.state}>
      <div class="operations-heading">
        <div>
          <p class="eyebrow">Agent operations</p>
          <h2 id="operations-title">Operations receipt</h2>
          <p>{operationsPresentation.detail}</p>
        </div>
        <div class="recovery-stamp" data-state={operationsPresentation.state} aria-label={`Operations state: ${operationsPresentation.label}`}>
          <span>DETERMINISTIC</span>
          <strong>{operationsPresentation.label}</strong>
        </div>
      </div>

      {#if operationsState === 'loading' || operationsState === 'idle'}
        <div class="operations-empty" aria-live="polite">Reading the bounded operations record…</div>
      {:else if operationsState === 'unavailable'}
        <div class="operations-empty operations-problem" role="alert">
          <strong>Operations view unavailable</strong>
          <p>{operationsError || operationsPresentation.detail}</p>
          <button class="quiet-button" onclick={() => loadOperations()}>Try again</button>
        </div>
      {:else}
        <div class="receipt-body">
          <dl class="receipt-facts">
            <div><dt>Observed</dt><dd>{formatTimestamp(operations?.observed_at)}</dd></div>
            <div><dt>Source receipt</dt><dd>{formatTimestamp(operations?.latest_source_received_at)}</dd></div>
            <div><dt>COP recovery</dt><dd>{operations?.recovery?.status || 'Not recorded'} · revision {formatNumber(operations?.recovery?.state_revision)}</dd></div>
            <div><dt>Projected</dt><dd>{formatTimestamp(operations?.recovery?.projected_at)}</dd></div>
            <div><dt>Service</dt><dd>{operations?.service?.version || 'Unknown'} · up {formatUptime(operations?.service?.uptime_seconds)}</dd></div>
            <div><dt>Local stream</dt><dd>{formatNumber(operations?.stream?.local_subscriber_count)} subscriber{Number(operations?.stream?.local_subscriber_count) === 1 ? '' : 's'} · {operations?.stream?.last_published?.name || 'no event published'}</dd></div>
            {#if operations?.stream?.last_published?.published_at}
              <div><dt>Last stream event</dt><dd>{formatTimestamp(operations.stream.last_published.published_at)}</dd></div>
            {/if}
          </dl>

          <div class="operations-ledgers">
            <section class="durable-tally" aria-labelledby="durable-tally-title">
              <div class="subsection-heading">
                <p class="eyebrow">Immutable record</p>
                <h3 id="durable-tally-title">Durable ledger</h3>
              </div>
              <dl>
                <div><dt>Raw</dt><dd>{formatNumber(operations?.counts?.raw_events)}</dd></div>
                <div><dt>Canonical</dt><dd>{formatNumber(operations?.counts?.canonical_events)}</dd></div>
                <div><dt>Projected</dt><dd>{formatNumber(operations?.counts?.projected_events)}</dd></div>
                <div><dt>Unprojected</dt><dd>{formatNumber(operations?.counts?.unprojected_events)}</dd></div>
                <div><dt>Checkpoints</dt><dd>{formatNumber(operations?.counts?.checkpoints)}</dd></div>
                <div><dt>Insights</dt><dd>{formatNumber(operations?.counts?.insights)}</dd></div>
                <div><dt>Recommendations</dt><dd>{formatNumber(operations?.counts?.recommendations)}</dd></div>
                <div><dt>Audit records</dt><dd>{formatNumber(operations?.counts?.audit_records)}</dd></div>
              </dl>
            </section>

            <section class="lifecycle-tally" aria-labelledby="lifecycle-tally-title">
              <div class="subsection-heading">
                <p class="eyebrow">Fixture normalizer</p>
                <h3 id="lifecycle-tally-title">Luna lifecycle</h3>
              </div>
              <dl>
                <div><dt>Accepted</dt><dd>{formatNumber(operations?.counts?.luna_lifecycle?.accepted)}</dd></div>
                <div><dt>Repaired</dt><dd>{formatNumber(operations?.counts?.luna_lifecycle?.repaired)}</dd></div>
                <div><dt>Quarantined</dt><dd>{formatNumber(operations?.counts?.luna_lifecycle?.quarantined)}</dd></div>
                <div><dt>Rejected</dt><dd>{formatNumber(operations?.counts?.luna_lifecycle?.rejected)}</dd></div>
              </dl>
              <p class="lifecycle-note">Lifecycle outcomes are recorded; only accepted and repaired records can enter deterministic projection.</p>
            </section>
          </div>

          <section class="model-record" aria-labelledby="model-record-title">
            <div class="subsection-heading">
              <p class="eyebrow">Structured-model record</p>
              <h3 id="model-record-title">Model runs <span>{formatNumber(operations?.counts?.model_runs?.total)}</span></h3>
            </div>
            <p>These are persisted invocation outcomes, not evidence of live Terra or Sol transport.</p>
            <dl class="model-run-list">
              {#each modelRunAgents as agent (agent.agent)}
                <div>
                  <dt>{agent.agent}</dt>
                  <dd><strong>{formatNumber(agent.total)} run{Number(agent.total) === 1 ? '' : 's'}</strong>{outcomeSummary(agent.validation_statuses)}</dd>
                </div>
              {/each}
            </dl>
          </section>

          <section class="capability-docket" aria-labelledby="capability-docket-title">
            <div class="capability-heading">
              <div>
                <p class="eyebrow">Capability docket</p>
                <h3 id="capability-docket-title">What is composed — and what is not</h3>
              </div>
              <p>Mode and status are statements of this demo’s current evidence boundary.</p>
            </div>
            <ul class="capability-cards">
              {#each operationsCapabilities as capability (capability.capability)}
                <li data-mode={capability.mode} data-status={capability.status}>
                  <div class="capability-card-topline">
                    <code>{capability.capability.replaceAll('_', ' ')}</code>
                    <span>{capability.mode.replaceAll('_', ' ')}</span>
                  </div>
                  <h4>{capability.feature}</h4>
                  <p>{capability.detail}</p>
                  <strong>{capability.status}</strong>
                </li>
              {/each}
            </ul>
          </section>

          <p class="operations-limits">No live Terra/Sol transport. No durable reconciliation worker. No external operational action.</p>
        </div>
      {/if}
    </section>
  </section>

  <aside class="evidence-rail" aria-label="Evidence and review">
    <section class="evidence-panel">
      <p class="eyebrow">Evidence resolution</p>
      <h2>{selectedEvidence?.label || 'Select a ledger item'}</h2>
      {#if evidenceState === 'idle'}
        <p class="panel-copy">Every evidence button calls the public resolver. Raw source payload bytes are never shown here by default.</p>
      {:else if evidenceState === 'loading'}
        <p class="panel-copy" aria-live="polite">Resolving cited artifact…</p>
      {:else if evidenceState === 'error'}
        <p class="panel-copy problem" role="alert">Evidence could not be resolved: {evidenceError}</p>
      {:else if evidenceState === 'unresolved' || !selectedEvidence?.resolved}
        <p class="panel-copy problem">This evidence reference is unresolved. {selectedEvidence?.reason || 'The API did not return a persisted artifact.'}</p>
      {:else}
        <dl class="resolution-meta">
          <div><dt>Kind</dt><dd>{selectedEvidence.kind}</dd></div>
          <div><dt>ID</dt><dd><code>{selectedEvidence.id}</code></dd></div>
          <div><dt>Status</dt><dd class="resolved">Resolved</dd></div>
        </dl>
        <pre aria-label="Resolved evidence artifact with raw payload bytes omitted">{evidenceText()}</pre>
      {/if}
    </section>

    <section class="review-panel">
      <p class="eyebrow">Public review</p>
      <p class="panel-copy">These public-demo calls append immutable audit records. They are visibly non-operational and never dispatch an external action.</p>
      <form onsubmit={submitBriefing}>
        <label for="briefing-note">Briefing request note</label>
        <textarea id="briefing-note" bind:value={briefingNote} rows="3"></textarea>
        <button class="review-button" disabled={actionState === 'loading'}>Record briefing request <span>executed: false</span></button>
      </form>
      <form onsubmit={submitAuditAction}>
        <label for="audit-action">Review action</label>
        <select id="audit-action" bind:value={auditAction}>
          <option value="acknowledged">Acknowledge</option>
          <option value="rejected">Reject</option>
          <option value="noted">Note</option>
        </select>
        <label for="audit-kind">Artifact kind</label>
        <select id="audit-kind" bind:value={auditTargetKind}>
          <option value="recommendation">Recommendation</option>
          <option value="insight">Insight</option>
        </select>
        <label for="audit-target">Resolvable artifact ID</label>
        <input id="audit-target" bind:value={auditTargetID} placeholder="recommendation-domestic-001" required />
        <label for="audit-note">Review note</label>
        <textarea id="audit-note" bind:value={auditNote} rows="3"></textarea>
        <button class="review-button" disabled={actionState === 'loading'}>Record review <span>executed: false</span></button>
      </form>
      {#if actionState !== 'idle'}
        <p class:problem={actionState === 'error'} class="action-result" aria-live="polite">{actionMessage || 'Recording immutable audit record…'}</p>
      {/if}
    </section>
  </aside>
</main>
