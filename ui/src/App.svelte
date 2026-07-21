<script>
  import { onMount } from 'svelte';
  import SimulationControls from './lib/SimulationControls.svelte';
  import IncidentWorkspace from './lib/IncidentWorkspace.svelte';
  import StatusDrawer from './lib/StatusDrawer.svelte';
  import ActionCards from './lib/ActionCards.svelte';
  import ProvenanceTab from './lib/ProvenanceTab.svelte';
  import HelpPanel from './lib/HelpPanel.svelte';
  import HelpTip from './lib/HelpTip.svelte';

  const defaultAPIBase = import.meta.env.VITE_MOSAIC_API_BASE_URL || '/api/v1';
  const hiddenArtifactFields = new Set(['payload_bytes_b64', 'raw_sha256']);

  let apiBase = $state(normaliseAPIBase(defaultAPIBase));
  let apiBaseInput = $state(normaliseAPIBase(defaultAPIBase));
  let health = $state({ state: 'idle', detail: 'Not checked' });
  let version = $state({ state: 'idle', detail: 'Not checked' });
  let openaiConfigured = $state(false);
  let cassetteMode = $state('passthrough');
  let cassetteDir = $state('');
  let modelUsage = $state(null);
  let modelUsageState = $state('idle');
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
  let auditTargetKind = $state('recommendation');
  let auditTargetID = $state('');
  let actionState = $state('idle');
  let actionMessage = $state('');

  // Simulation Session State
  let session = $state(null);
  let elapsedSeconds = $state(0);
  let activeTab = $state('workspace'); // 'workspace' | 'provenance'
  let helpOpen = $state(false);
  let maintenanceNote = $state('');

  function prefillMaintenance(noteText) {
    maintenanceNote = noteText;
  }

  let streamController;
  let reconnectTimer;
  let reconnectAttempt = 0;

  let simStreamController;
  let simReconnectTimer;
  let simReconnectAttempt = 0;

  let destroyed = false;

  let operationsPresentation = $derived(describeOperations(operations, operationsState));

  onMount(() => {
    void refreshAll();
    return () => {
      destroyed = true;
      stopStream();
      stopSimulationStream();
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
    await Promise.all([
      loadPublicStatus(),
      loadCOP(),
      loadOperations(),
      loadAdvisories(),
      loadSimulationStatus()
    ]);
    connectStream();
    connectSimulationStream();
  }

  async function loadPublicStatus() {
    health = { state: 'loading', detail: 'Checking health' };
    version = { state: 'loading', detail: 'Checking version' };
    modelUsageState = 'loading';
    const [healthResult, versionResult, modelUsageResult] = await Promise.allSettled([
      readEnvelope('health'),
      readEnvelope('version'),
      readEnvelope('model-usage')
    ]);
    health = healthResult.status === 'fulfilled'
      ? { state: 'ready', detail: healthResult.value?.status || 'ok' }
      : { state: 'error', detail: healthResult.reason.message };
    version = versionResult.status === 'fulfilled'
      ? { state: 'ready', detail: versionResult.value?.version || 'Unknown version' }
      : { state: 'error', detail: versionResult.reason.message };
    if (versionResult.status === 'fulfilled') {
      openaiConfigured = !!versionResult.value?.openai_configured;
      const mode = String(versionResult.value?.cassette_mode || '').trim().toLowerCase();
      cassetteMode = mode || 'passthrough';
      cassetteDir = String(versionResult.value?.cassette_dir || '').trim();
    }
    if (modelUsageResult.status === 'fulfilled') {
      modelUsage = modelUsageResult.value;
      modelUsageState = 'ready';
    } else {
      modelUsage = null;
      modelUsageState = 'error';
    }
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
      if (advisories?.cassette_mode) {
        cassetteMode = String(advisories.cassette_mode).trim().toLowerCase() || cassetteMode;
      }
      if (advisories?.status === 'unavailable') {
        advisoriesState = 'unavailable';
      } else {
        const insightsArr = Array.isArray(advisories?.insights) ? advisories.insights : [];
        const recsArr = Array.isArray(advisories?.recommendations) ? advisories.recommendations : [];
        if (insightsArr.length === 0 && recsArr.length === 0) {
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

  async function loadSimulationStatus() {
    try {
      session = await readEnvelope('simulation/status');
      if (session?.status !== 'running') {
        elapsedSeconds = 0;
      }
    } catch (error) {
      session = null;
      elapsedSeconds = 0;
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
    void loadCOP();
    void loadOperations({ quiet: true });
    void loadAdvisories();
  }

  function stopSimulationStream() {
    if (simReconnectTimer) {
      clearTimeout(simReconnectTimer);
      simReconnectTimer = undefined;
    }
    if (simStreamController) {
      simStreamController.abort();
      simStreamController = undefined;
    }
  }

  async function connectSimulationStream() {
    stopSimulationStream();
    const controller = new AbortController();
    simStreamController = controller;

    try {
      const response = await fetch(apiURL('simulation/stream'), {
        headers: headersFor(),
        signal: controller.signal
      });
      if (!response.ok || !response.body) {
        let detail = `The simulation stream returned ${response.status}.`;
        try {
          const body = await response.json();
          detail = body?.error?.message || detail;
        } catch {
        }
        throw new Error(detail);
      }
      simReconnectAttempt = 0;
      await consumeSimulationSSE(response.body, controller.signal);
      if (!controller.signal.aborted) {
        throw new Error('The simulation stream ended.');
      }
    } catch (error) {
      if (controller.signal.aborted || destroyed || simStreamController !== controller) {
        return;
      }
      scheduleSimReconnect();
    }
  }

  function scheduleSimReconnect() {
    if (destroyed) {
      return;
    }
    const delay = Math.min(1000 * 2 ** simReconnectAttempt, 12000);
    simReconnectAttempt += 1;
    simReconnectTimer = setTimeout(() => {
      simReconnectTimer = undefined;
      void connectSimulationStream();
    }, delay);
  }

  async function consumeSimulationSSE(body, signal) {
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
      applySimulationStreamEvent(eventName, payload);
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

  function applySimulationStreamEvent(name, payload) {
    if (name === 'session.snapshot') {
      session = payload;
      if (payload?.status !== 'running') {
        elapsedSeconds = 0;
      }
      return;
    }
    if (name === 'workspace_clear') {
      // Session restart clears the live view; keep a short empty state until
      // beats/stream snapshots refill, then restore seeded COP if nothing arrives.
      cop = null;
      advisories = null;
      elapsedSeconds = 0;
      return;
    }
    if (name === 'status_change') {
      const newStatus = payload.payload?.status || payload.status;
      if (session) {
        session.status = newStatus;
      } else {
        session = { session_id: payload.session_id, status: newStatus, beats: [] };
      }
      if (newStatus !== 'running') {
        elapsedSeconds = 0;
      }
      if (newStatus === 'ended' || newStatus === 'idle' || newStatus === 'pending') {
        void loadCOP();
        void loadAdvisories();
      }
      return;
    }
    if (name === 'beat') {
      if (session) {
        if (!session.beats) {
          session.beats = [];
        }
        const b = payload.payload;
        if (b && !session.beats.some(x => x.beat_id === b.beat_id)) {
          session.beats.push(b);
        }
      }
      // Beats often land with COP/stream updates; refresh facts for the demo view.
      void loadCOP();
      void loadAdvisories();
      return;
    }
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

  function formatNumber(value) {
    const number = Number(value);
    return Number.isFinite(number) ? new Intl.NumberFormat().format(number) : '—';
  }

  // filter payload fields for public audit
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

  // Flatten the artifact's primitive fields into label/value rows so the
  // right rail reads as a summary instead of a raw JSON dump. Nested
  // structures stay available under "View raw record".
  function evidenceSummary() {
    const artifact = selectedEvidence?.artifact;
    if (!artifact || typeof artifact !== 'object' || Array.isArray(artifact)) return [];
    const rows = [];
    for (const [key, value] of Object.entries(safeArtifact(artifact))) {
      if (value === null || value === undefined) continue;
      if (typeof value === 'object') continue;
      rows.push({ key, label: key.replaceAll('_', ' '), value: String(value) });
      if (rows.length >= 12) break;
    }
    return rows;
  }

  function formatClock(total) {
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const seconds = total % 60;
    const pad = (n) => String(n).padStart(2, '0');
    return `${pad(hours)}:${pad(minutes)}:${pad(seconds)}`;
  }

  // Left-rail status board + top-bar chips, derived from the projection.
  let copSnapshot = $derived(cop?.cop || cop);
  let railIncidents = $derived(listOf(copSnapshot?.incidents));
  let railUnits = $derived(listOf(copSnapshot?.units));
  let railRoads = $derived(listOf(copSnapshot?.roads));
  let railResources = $derived(listOf(copSnapshot?.resources));
  let railWeather = $derived(listOf(copSnapshot?.weather_alerts));
  let activeIncidentID = $derived(railIncidents[0]?.incident_id || '');

  function listOf(value) {
    return Array.isArray(value) ? value : [];
  }

  function availabilityTone(availability) {
    const value = String(availability || '').toLowerCase();
    if (value.includes('available')) return 'ok';
    if (value.includes('assigned') || value.includes('scene') || value.includes('route') || value.includes('dispatch')) return 'info';
    if (value.includes('unavailable') || value.includes('out')) return 'alert';
    return 'warn';
  }

  function roadTone(status) {
    const value = String(status || '').toLowerCase();
    if (value.includes('open')) return 'ok';
    if (value.includes('closed') || value.includes('blocked')) return 'alert';
    return 'warn';
  }

  function weatherTone(alert) {
    const status = String(alert?.status || '').toLowerCase();
    if (status.includes('cleared') || status.includes('expired')) return 'ok';
    const severity = String(alert?.severity || '').toLowerCase();
    if (severity.includes('severe') || severity.includes('extreme') || severity.includes('high')) return 'alert';
    return 'warn';
  }
</script>

<svelte:head>
  <title>Mosaic — Domestic disturbance demo</title>
</svelte:head>

<header class="masthead">
  <a class="wordmark" href="#cop" aria-label="Mosaic home">
    <span class="wordmark-mark" aria-hidden="true"></span>
    <span>Mosaic</span>
    <small>operator demo</small>
  </a>
  <div class="topbar-meta">
    <span class="meta-chip synthetic">Synthetic data · training</span>
    <span class="meta-chip">Inc <strong>{activeIncidentID || '—'}</strong></span>
    {#if session?.status === 'running'}
      <span class="meta-chip clock">T+ <strong>{formatClock(elapsedSeconds)}</strong></span>
    {/if}
  </div>
  <div class="masthead-actions">
    <button
      type="button"
      class="help-open-btn"
      onclick={() => (helpOpen = true)}
      aria-haspopup="dialog"
      aria-expanded={helpOpen}
    >
      How this works
    </button>
    <div class="connection-pill" data-state={openaiConfigured ? 'live' : 'idle'} aria-live="polite" style="margin-right: 0.5rem;">
      <span aria-hidden="true"></span>
      {openaiConfigured ? 'AI: Live' : 'AI: Demo pack'}
    </div>
    <div class="connection-pill" data-state={streamState} aria-live="polite">
      <span aria-hidden="true"></span>
      {streamState === 'live' ? 'Connected' : streamState === 'reconnecting' ? 'Reconnecting' : 'Checking'}
    </div>
  </div>
</header>

<HelpPanel open={helpOpen} onClose={() => (helpOpen = false)} />

<main class="folio">
  <aside class="context-rail" aria-label="Status board">
    <section class="rail-section">
      <p class="eyebrow">
        Status board
        <HelpTip text="Live roster from the incident board: units, roads, resources, and weather. Fills in as the scenario plays." label="About the status board" />
      </p>

      <div class="rail-block" aria-label="Units">
        <p class="rail-block-title">Units <span class="count">{railUnits.length}</span></p>
        {#if railUnits.length === 0}
          <p class="rail-empty">No units on board</p>
        {:else}
          <ul class="status-lines">
            {#each railUnits as unit (unit.unit_id)}
              <li class="status-line">
                <span class="sl-id">{unit.unit_id}{#if unit.incident_id}<span class="sl-note">→ {unit.incident_id}</span>{/if}</span>
                <span class="sl-state" data-tone={availabilityTone(unit.availability)}>{unit.availability || 'unknown'}</span>
              </li>
            {/each}
          </ul>
        {/if}
      </div>

      <div class="rail-block" aria-label="Roads">
        <p class="rail-block-title">Roads <span class="count">{railRoads.length}</span></p>
        {#if railRoads.length === 0}
          <p class="rail-empty">No road reports</p>
        {:else}
          <ul class="status-lines">
            {#each railRoads as road (road.road_id)}
              <li class="status-line">
                <span class="sl-id">{road.name || road.road_id}</span>
                <span class="sl-state" data-tone={roadTone(road.status)}>{road.status || 'unknown'}</span>
              </li>
            {/each}
          </ul>
        {/if}
      </div>

      <div class="rail-block" aria-label="Resources">
        <p class="rail-block-title">Resources <span class="count">{railResources.length}</span></p>
        {#if railResources.length === 0}
          <p class="rail-empty">No resources on board</p>
        {:else}
          <ul class="status-lines">
            {#each railResources as resource (resource.resource_id)}
              <li class="status-line">
                <span class="sl-id">{resource.resource_id}{#if resource.incident_id}<span class="sl-note">→ {resource.incident_id}</span>{/if}</span>
                <span class="sl-state" data-tone={availabilityTone(resource.availability)}>{resource.availability || 'unknown'}</span>
              </li>
            {/each}
          </ul>
        {/if}
      </div>

      <div class="rail-block" aria-label="Weather">
        <p class="rail-block-title">Weather <span class="count">{railWeather.length}</span></p>
        {#if railWeather.length === 0}
          <p class="rail-empty">No active alerts</p>
        {:else}
          <ul class="status-lines">
            {#each railWeather as alert (alert.weather_alert_id)}
              <li class="status-line">
                <span class="sl-id">{alert.summary || alert.weather_alert_id}</span>
                <span class="sl-state" data-tone={weatherTone(alert)}>{alert.status || alert.severity || 'unknown'}</span>
              </li>
            {/each}
          </ul>
        {/if}
      </div>

      <button type="button" class="quiet-button help-rail-btn" onclick={() => (helpOpen = true)}>
        How this works
      </button>
    </section>

    <section class="rail-section boundary-note">
      <p class="eyebrow">
        Advisory only
        <HelpTip text="AI can suggest context, but only you decide. Your notes are saved to the demo log, not acted on." label="About safety rule" />
      </p>
      <p>
        Mosaic never contacts Dispatch, Maintenance, or any live system.
      </p>
    </section>
  </aside>

  <div class="center-workspace">
    <!-- Simulation Controls at the top of the workspace -->
    <SimulationControls
      bind:session
      bind:elapsedSeconds
      {readEnvelope}
      bind:actionState
      bind:actionMessage
      {cassetteMode}
      {loadAdvisories}
    />

    <!-- Tab switcher navigation -->
    <div class="workspace-tabs-nav">
      <button class="tab-nav-btn" class:active={activeTab === 'workspace'} onclick={() => activeTab = 'workspace'}>
        Live incident board
        <HelpTip text="Main demo screen: play the scenario, see the current picture, and review advice." label="About live incident board" direction="bottom" />
      </button>
      <button class="tab-nav-btn" class:active={activeTab === 'provenance'} onclick={() => activeTab = 'provenance'}>
        Decision history
        <HelpTip text="Everything you and the demo models recorded: notes, handoffs, and analysis runs — for review only." label="About decision history" direction="bottom" />
      </button>
    </div>

    {#if activeTab === 'workspace'}
      <!-- Incident Workspace in the middle -->
      <IncidentWorkspace
        {cop}
        {copState}
        {copError}
        {advisories}
        {advisoriesState}
        {advisoriesError}
        {elapsedSeconds}
        {loadAdvisories}
        {selectEvidence}
        {modelUsage}
        {cassetteMode}
        bind:auditTargetID
        bind:auditTargetKind
        onPrefillMaintenance={prefillMaintenance}
      />
    {:else}
      <!-- Provenance Tab View -->
      <ProvenanceTab
        {session}
        {advisories}
        {selectEvidence}
      />
    {/if}
  </div>

  <aside class="evidence-rail" aria-label="Evidence and review">
    <section class="evidence-panel">
      <p class="eyebrow">
        Where did this come from?
        <HelpTip text="Click “Show source” on any fact to open the underlying demo record. Raw wire dumps stay hidden." label="About evidence panel" />
      </p>
      <h2>{selectedEvidence?.label || 'Click “Show source” on a fact'}</h2>
      {#if evidenceState === 'idle'}
        <p class="panel-copy">
          Use this panel to check the story behind a road closure, weather alert,
          or assessment. You are reading demo records — not a live CAD feed.
        </p>
      {:else if evidenceState === 'loading'}
        <p class="panel-copy" aria-live="polite">Looking up the source record…</p>
      {:else if evidenceState === 'error'}
        <p class="panel-copy problem" role="alert">Could not load that source: {evidenceError}</p>
      {:else if evidenceState === 'unresolved' || !selectedEvidence?.resolved}
        <p class="panel-copy problem">No matching source was found. {selectedEvidence?.reason || 'It may have been cleared when the scenario restarted.'}</p>
      {:else}
        <dl class="resolution-meta">
          <div><dt>Type</dt><dd>{selectedEvidence.kind}</dd></div>
          <div><dt>Record</dt><dd><code>{selectedEvidence.id}</code></dd></div>
          <div><dt>Status</dt><dd class="resolved">Found</dd></div>
        </dl>
        {#if evidenceSummary().length > 0}
          <dl class="artifact-summary" aria-label="Source record summary">
            {#each evidenceSummary() as row (row.key)}
              <div><dt>{row.label}</dt><dd>{row.value}</dd></div>
            {/each}
          </dl>
        {/if}
        <details class="raw-record">
          <summary>View raw record</summary>
          <pre aria-label="Source record without raw payload bytes">{evidenceText()}</pre>
        </details>
      {/if}
    </section>

    <ActionCards
      {readEnvelope}
      {cop}
      {advisories}
      {selectEvidence}
      bind:auditTargetID
      bind:auditTargetKind
      bind:actionState
      bind:actionMessage
      bind:maintenanceNote={maintenanceNote}
    />
  </aside>
</main>

<!-- Collapsible Developer Status Drawer at the bottom -->
<StatusDrawer
  {health}
  {version}
  {streamState}
  {streamDetail}
  {operations}
  {operationsState}
  {operationsError}
  {operationsPresentation}
  {modelUsage}
  {modelUsageState}
  {cassetteMode}
  {cassetteDir}
  bind:apiBaseInput
  {applyAPIBase}
  loadOperations={() => loadOperations()}
/>
