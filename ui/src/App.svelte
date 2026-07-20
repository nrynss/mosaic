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
  <p class="scope">Synthetic 911 scenario · practice only · nothing is sent outside</p>
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
      {openaiConfigured ? 'AI Key: Active' : 'AI Key: Demo pack'}
      <HelpTip text={openaiConfigured ? 'A live AI key is configured on the server, so Terra and Sol can call OpenAI. If the key runs out of credit, calls fall back to the demo pack. See the developer console for an estimated-spend readout.' : 'Live models are offline. The dashboard runs on the pre-built demo pack.'} label="About OpenAI API key status" direction="bottom" align="end" />
    </div>
    <div class="connection-pill" data-state={streamState} aria-live="polite">
      <span aria-hidden="true"></span>
      {streamState === 'live' ? 'Connected' : streamState === 'reconnecting' ? 'Reconnecting' : 'Checking'}
      <HelpTip text="Green means the browser is connected for live updates. If it drops, Mosaic retries automatically." label="About connection status" direction="bottom" align="end" />
    </div>
  </div>
</header>

<HelpPanel open={helpOpen} onClose={() => (helpOpen = false)} />

<main class="folio">
  <aside class="context-rail" aria-label="Demo context">
    <section class="rail-section">
      <p class="eyebrow">You are the operator</p>
      <h1>One synthetic call.<br />Your judgment stays human.</h1>
      <p class="rail-copy">
        This is a practice board for a made-up domestic-disturbance call.
        Press <strong>Play scenario</strong>, watch facts arrive, review advice,
        and record notes. Nothing here pages a real agency.
      </p>
      <button type="button" class="quiet-button help-rail-btn" onclick={() => (helpOpen = true)}>
        How this works (walkthrough)
      </button>
    </section>

    <section class="rail-section boundary-note">
      <p class="eyebrow">
        Safety rule
        <HelpTip text="AI can suggest context, but only you decide. Your notes are saved to the demo log, not acted on." label="About safety rule" />
      </p>
      <p>
        What you see is the demo’s current picture of the synthetic incident.
        Mosaic will not contact Dispatch, Maintenance, or any live system.
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
        <pre aria-label="Source record without raw payload bytes">{evidenceText()}</pre>
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
  bind:apiBaseInput
  {applyAPIBase}
  loadOperations={() => loadOperations()}
/>
