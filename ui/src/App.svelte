<script>
  import { onMount } from 'svelte';

  const identityHeader = 'X-Mosaic-Demo-Identity';
  const defaultAPIBase = import.meta.env.VITE_MOSAIC_API_BASE_URL || '/api/v1';
  const hiddenArtifactFields = new Set(['payload_bytes_b64', 'raw_sha256']);

  let apiBase = $state(normaliseAPIBase(defaultAPIBase));
  let apiBaseInput = $state(normaliseAPIBase(defaultAPIBase));
  let identity = $state('viewer-demo');
  let health = $state({ state: 'idle', detail: 'Not checked' });
  let version = $state({ state: 'idle', detail: 'Not checked' });
  let cop = $state(null);
  let copState = $state('idle');
  let copError = $state('');
  let streamState = $state('idle');
  let streamDetail = $state('Not connected');
  let selectedEvidence = $state(null);
  let evidenceState = $state('idle');
  let evidenceError = $state('');
  let briefingNote = $state('Request a structured supervisor briefing for the current synthetic COP.');
  let auditAction = $state('acknowledged');
  let auditTargetKind = $state('recommendation');
  let auditTargetID = $state('');
  let auditNote = $state('Supervisor review recorded for the synthetic demonstration.');
  let actionState = $state('idle');
  let actionMessage = $state('');

  let streamController;
  let reconnectTimer;
  let reconnectAttempt = 0;
  let destroyed = false;

  let isSupervisor = $derived(identity === 'supervisor-demo');
  let roleLabel = $derived(isSupervisor ? 'Supervisor demo identity' : 'Viewer demo identity');
  let copStateRevision = $derived(cop?.state_revision ?? cop?.cop?.state_revision ?? '—');
  let claimItems = $derived(makeClaimItems(cop?.cop));

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

  function headersFor(identityRequired = true, additional = {}) {
    const headers = new Headers(additional);
    if (identityRequired) {
      headers.set(identityHeader, identity);
    }
    return headers;
  }

  async function readEnvelope(path, options = {}, identityRequired = true) {
    const response = await fetch(apiURL(path), {
      ...options,
      headers: headersFor(identityRequired, options.headers)
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
    await Promise.all([loadPublicStatus(), loadCOP()]);
    connectStream();
  }

  async function loadPublicStatus() {
    health = { state: 'loading', detail: 'Checking health' };
    version = { state: 'loading', detail: 'Checking version' };
    const [healthResult, versionResult] = await Promise.allSettled([
      readEnvelope('health', {}, false),
      readEnvelope('version', {}, false)
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

  function selectIdentity(nextIdentity) {
    if (identity === nextIdentity) {
      return;
    }
    identity = nextIdentity;
    selectedEvidence = null;
    evidenceState = 'idle';
    evidenceError = '';
    void refreshAll();
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
      return;
    }
    // P08's named event means the read model changed. Re-reading the COP keeps
    // the dashboard deterministic even when an event's payload is only a notice.
    void loadCOP();
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
  <p class="scope">Synthetic demo · local only · no operational actions</p>
  <div class="connection-pill" data-state={streamState} aria-live="polite">
    <span aria-hidden="true"></span>
    {streamState === 'live' ? 'Live' : streamState === 'reconnecting' ? 'Reconnecting' : 'Checking'}
  </div>
</header>

<main class="folio">
  <aside class="context-rail" aria-label="Demo context">
    <section class="rail-section">
      <p class="eyebrow">Identity</p>
      <h1>Read the record.<br />Keep judgment human.</h1>
      <p class="rail-copy">{roleLabel}. The identity is a fixed demo control, not production authentication.</p>
      <div class="identity-switch" aria-label="Choose demo identity">
        <button class:active={identity === 'viewer-demo'} aria-pressed={identity === 'viewer-demo'} onclick={() => selectIdentity('viewer-demo')}>
          <span>Viewer</span><small>Inspect only</small>
        </button>
        <button class:active={identity === 'supervisor-demo'} aria-pressed={identity === 'supervisor-demo'} onclick={() => selectIdentity('supervisor-demo')}>
          <span>Supervisor</span><small>Record review</small>
        </button>
      </div>
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

    <section class="withheld-claims" aria-label="Assessment and recommendation availability">
      <div>
        <p class="claim-class assessed">Derived assessment</p>
        <h3>No assessment artifact is exposed by this read surface.</h3>
        <p>The dashboard does not infer an assessment from reported facts. It will show a Terra artifact only when an evidence-resolvable API record is available.</p>
      </div>
      <div>
        <p class="claim-class recommended">Human-review recommendation</p>
        <h3>No recommendation artifact is exposed by this read surface.</h3>
        <p>A supervisor may record a review only against an existing recommendation or insight ID. This never executes an operational action.</p>
      </div>
    </section>
  </section>

  <aside class="evidence-rail" aria-label="Evidence and review">
    <section class="evidence-panel">
      <p class="eyebrow">Evidence resolution</p>
      <h2>{selectedEvidence?.label || 'Select a ledger item'}</h2>
      {#if evidenceState === 'idle'}
        <p class="panel-copy">Every evidence button calls the authenticated P08 resolver. Raw source payload bytes are never shown here by default.</p>
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
      <p class="eyebrow">Supervisor review</p>
      {#if isSupervisor}
        <p class="panel-copy">These calls append immutable demo audit records. They are visibly non-operational.</p>
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
      {:else}
        <p class="panel-copy">Viewer identity has no action controls. Select the fixed supervisor demo identity to record a non-operational briefing request or review.</p>
      {/if}
    </section>
  </aside>
</main>
