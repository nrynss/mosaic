<script>
  import { onMount } from 'svelte';
  import SimulationControls from './lib/SimulationControls.svelte';
  import IncidentWorkspace from './lib/IncidentWorkspace.svelte';
  import StatusDrawer from './lib/StatusDrawer.svelte';
  import ActionCards from './lib/ActionCards.svelte';

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
  let auditTargetKind = $state('recommendation');
  let auditTargetID = $state('');
  let actionState = $state('idle');
  let actionMessage = $state('');

  // Simulation Session State
  let session = $state(null);
  let elapsedSeconds = $state(0);

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

    <section class="rail-section boundary-note">
      <p class="eyebrow">Boundary</p>
      <p>Claims describe the current deterministic record. Mosaic does not dispatch, contact, or alter an operational system.</p>
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
    />

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
      bind:auditTargetID
      bind:auditTargetKind
    />
  </div>

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

    <ActionCards
      {readEnvelope}
      {cop}
      {advisories}
      {selectEvidence}
      bind:auditTargetID
      bind:auditTargetKind
      bind:actionState
      bind:actionMessage
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
  bind:apiBaseInput
  {applyAPIBase}
  loadOperations={() => loadOperations()}
/>
