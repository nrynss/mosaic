<script>
  let {
    readEnvelope,
    cop,
    advisories,
    selectEvidence,
    auditTargetID = $bindable(''),
    auditTargetKind = $bindable('recommendation'),
    actionState = $bindable('idle'),
    actionMessage = $bindable(''),
    maintenanceNote = $bindable('Operator road condition notes.')
  } = $props();

  // Local state for Dispatch Handoff
  let dispatchObservations = $state('Operator notes on dispatch context.');
  let dispatchResult = $state(null);
  let dispatchError = $state('');

  // Local state for Maintenance Handoff
  let maintenanceResult = $state(null);
  let maintenanceError = $state('');

  // Local state for Operator Decision Controls
  let decisionNote = $state('Public demo review recorded for the synthetic demonstration.');
  let decisionResult = $state(null);
  let decisionError = $state('');

  // Deriving active incident context from COP
  let activeIncident = $derived(
    Array.isArray(cop?.cop?.incidents || cop?.incidents)
      ? (cop?.cop?.incidents || cop?.incidents)[0]
      : null
  );

  // Deriving roads context from COP
  let roads = $derived(
    Array.isArray(cop?.cop?.roads || cop?.roads)
      ? (cop?.cop?.roads || cop?.roads)
      : []
  );

  // POST handle for dispatch handoff
  async function handleDispatchHandoff(e) {
    if (e) e.preventDefault();
    actionState = 'loading';
    actionMessage = 'Preparing dispatch handoff...';
    dispatchError = '';
    dispatchResult = null;
    try {
      const res = await readEnvelope('operator/prepare-handoff', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          recipient: 'dispatch',
          target_kind: 'system',
          target_id: 'operator-dispatch-handoff',
          note: dispatchObservations
        })
      });
      dispatchResult = res;
      actionState = 'ready';
      actionMessage = `Dispatch handoff prepared. Executed: ${res.executed}, Delivered: ${res.delivered}`;
    } catch (err) {
      dispatchError = err.message;
      actionState = 'error';
      actionMessage = err.message;
    }
  }

  // POST handle for maintenance handoff
  async function handleMaintenanceHandoff(e) {
    if (e) e.preventDefault();
    actionState = 'loading';
    actionMessage = 'Preparing maintenance handoff...';
    maintenanceError = '';
    maintenanceResult = null;
    try {
      const res = await readEnvelope('operator/prepare-handoff', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          recipient: 'maintenance',
          target_kind: 'system',
          target_id: 'operator-maintenance-handoff',
          note: maintenanceNote
        })
      });
      maintenanceResult = res;
      actionState = 'ready';
      actionMessage = `Maintenance handoff prepared. Executed: ${res.executed}, Delivered: ${res.delivered}`;
    } catch (err) {
      maintenanceError = err.message;
      actionState = 'error';
      actionMessage = err.message;
    }
  }

  // POST handle for decision (approve or annotate)
  async function handleDecision(actionType) {
    actionState = 'loading';
    actionMessage = `Recording operator decision (${actionType})...`;
    decisionError = '';
    decisionResult = null;

    const endpoint = actionType === 'approve' ? 'operator/approve' : 'operator/annotate';
    try {
      const res = await readEnvelope(endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          target_kind: auditTargetKind,
          target_id: auditTargetID,
          note: decisionNote
        })
      });
      decisionResult = {
        action: actionType,
        executed: res.executed,
        audit_record_id: res.audit_record?.audit_record_id || 'N/A',
        audit_record: res.audit_record
      };
      actionState = 'ready';
      actionMessage = `Review ${actionType} recorded. Executed: ${res.executed}`;
    } catch (err) {
      decisionError = err.message;
      actionState = 'error';
      actionMessage = err.message;
    }
  }
</script>

<div class="action-cards-panel">
  <!-- Header -->
  <div class="panel-section-header">
    <span class="eyebrow">Recipient Action Handoffs</span>
    <h3>Operator Handoff Controls</h3>
    <p class="section-desc">Prepare bounded notifications for departmental review. Actions are recorded but not physically sent.</p>
  </div>

  <!-- Dispatch Card -->
  <div class="action-card dispatch-card">
    <div class="card-header">
      <span class="badge recipient-badge">RECIPIENT: DISPATCH</span>
      <span class="badge status-badge safety-badge">recorded, not sent</span>
    </div>

    <div class="context-box">
      <span class="context-title">INCIDENT CONTEXT</span>
      {#if activeIncident}
        <div class="context-grid">
          <div><span class="lbl">ID:</span> <code class="val">{activeIncident.incident_id}</code></div>
          <div><span class="lbl">Cat:</span> <span class="val">{activeIncident.category || '—'}</span></div>
          <div><span class="lbl">Loc:</span> <span class="val">{activeIncident.location_id || '—'}</span></div>
        </div>
      {:else}
        <p class="context-empty">Waiting for active incident data...</p>
      {/if}
    </div>

    <form onsubmit={handleDispatchHandoff} class="card-form">
      <label for="dispatch-observations">Observations & Notes</label>
      <textarea
        id="dispatch-observations"
        bind:value={dispatchObservations}
        rows="3"
        placeholder="Enter dispatcher briefing notes..."
      ></textarea>
      <button class="action-btn" type="submit" disabled={actionState === 'loading'}>
        Prepare dispatch handoff
      </button>
    </form>

    {#if dispatchResult}
      <div class="result-box success">
        <div class="res-row"><strong>executed:</strong> <span>{dispatchResult.executed}</span></div>
        <div class="res-row"><strong>delivered:</strong> <span>{dispatchResult.delivered}</span></div>
        <div class="res-row"><strong>status:</strong> <span>{dispatchResult.handoff_status}</span></div>
        <p class="res-msg">{dispatchResult.message}</p>
      </div>
    {/if}
    {#if dispatchError}
      <div class="result-box failure">
        <p class="res-msg problem">{dispatchError}</p>
      </div>
    {/if}
  </div>

  <!-- Maintenance Card -->
  <div class="action-card maintenance-card">
    <div class="card-header">
      <span class="badge recipient-badge">RECIPIENT: MAINTENANCE</span>
      <span class="badge status-badge safety-badge">recorded, not sent</span>
    </div>

    <div class="context-box">
      <span class="context-title">ROAD CONDITIONS</span>
      {#if roads.length > 0}
        <ul class="roads-list">
          {#each roads.slice(0, 3) as road}
            <li>
              <span class="road-name">{road.name || road.road_id}:</span>
              <span class="road-status" class:closed={road.status === 'closed'}>{road.status || 'unknown'}</span>
            </li>
          {/each}
          {#if roads.length > 3}
            <li class="more-roads">+ {roads.length - 3} more roads</li>
          {/if}
        </ul>
      {:else}
        <p class="context-empty">No active road conditions reported.</p>
      {/if}
    </div>

    <form onsubmit={handleMaintenanceHandoff} class="card-form">
      <label for="maintenance-note">Condition Annotations</label>
      <textarea
        id="maintenance-note"
        bind:value={maintenanceNote}
        rows="3"
        placeholder="Enter infrastructure warning details..."
      ></textarea>
      <button class="action-btn" type="submit" disabled={actionState === 'loading'}>
        Prepare maintenance handoff
      </button>
    </form>

    {#if maintenanceResult}
      <div class="result-box success">
        <div class="res-row"><strong>executed:</strong> <span>{maintenanceResult.executed}</span></div>
        <div class="res-row"><strong>delivered:</strong> <span>{maintenanceResult.delivered}</span></div>
        <div class="res-row"><strong>status:</strong> <span>{maintenanceResult.handoff_status}</span></div>
        <p class="res-msg">{maintenanceResult.message}</p>
      </div>
    {/if}
    {#if maintenanceError}
      <div class="result-box failure">
        <p class="res-msg problem">{maintenanceError}</p>
      </div>
    {/if}
  </div>

  <!-- Decision Controls Panel -->
  <div class="decision-panel">
    <div class="panel-section-header">
      <span class="eyebrow">Public Review & Decisions</span>
      <h3>Operator Decisions</h3>
      <p class="section-desc">Record review actions. No live external department will be contacted.</p>
    </div>

    <div class="decision-form card-form">
      <div class="input-grid">
        <div>
          <label for="decision-kind">Target Kind</label>
          <select id="decision-kind" bind:value={auditTargetKind}>
            <option value="recommendation">Recommendation</option>
            <option value="insight">Insight</option>
            <option value="briefing">Briefing</option>
            <option value="system">System</option>
          </select>
        </div>
        <div>
          <label for="decision-target">Target ID</label>
          <input
            id="decision-target"
            bind:value={auditTargetID}
            placeholder="e.g. rec-001"
            required
          />
        </div>
      </div>

      <label for="decision-note">Decision Note</label>
      <textarea
        id="decision-note"
        bind:value={decisionNote}
        rows="3"
        placeholder="Enter review annotations..."
      ></textarea>

      <div class="btn-group">
        <button
          class="decision-btn approve-btn"
          type="button"
          onclick={() => handleDecision('approve')}
          disabled={actionState === 'loading'}
        >
          Approve <span>executed: false</span>
        </button>
        <button
          class="decision-btn annotate-btn"
          type="button"
          onclick={() => handleDecision('annotate')}
          disabled={actionState === 'loading'}
        >
          Annotate <span>executed: false</span>
        </button>
      </div>
    </div>

    {#if decisionResult}
      <div class="result-box success">
        <div class="res-row"><strong>action:</strong> <span class="capitalize">{decisionResult.action}</span></div>
        <div class="res-row"><strong>executed:</strong> <span>{decisionResult.executed}</span></div>
        <div class="res-row"><strong>audit ID:</strong> <code>{decisionResult.audit_record_id}</code></div>
      </div>
    {/if}
    {#if decisionError}
      <div class="result-box failure">
        <p class="res-msg problem">{decisionError}</p>
      </div>
    {/if}
  </div>
</div>

<style>
  .action-cards-panel {
    display: grid;
    gap: 1.5rem;
    padding-top: 1rem;
  }

  .panel-section-header {
    border-bottom: 1px solid rgba(158, 184, 192, 0.4);
    padding-bottom: 0.5rem;
    margin-bottom: 0.5rem;
  }

  .panel-section-header h3 {
    font-size: 1.25rem;
    color: #173342;
    margin: 0.2rem 0;
  }

  .section-desc {
    font-size: 0.78rem;
    color: #5d6d7e;
    margin: 0;
    line-height: 1.4;
  }

  .action-card {
    background: #fdfefe;
    border: 1px solid #c9d6d9;
    border-top: 3px solid #c9872f;
    padding: 1rem;
    display: grid;
    gap: 0.8rem;
    box-shadow: 0 2px 4px rgba(0,0,0,0.03);
  }

  .card-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    border-bottom: 1px solid rgba(158, 184, 192, 0.3);
    padding-bottom: 0.5rem;
  }

  .badge {
    font-family: "Cascadia Code", ui-monospace, monospace;
    font-size: 0.62rem;
    font-weight: bold;
    padding: 0.15rem 0.4rem;
  }

  .recipient-badge {
    color: #173342;
    background: rgba(23, 51, 66, 0.08);
    border: 1px solid rgba(23, 51, 66, 0.15);
  }

  .status-badge.safety-badge {
    color: #943a32;
    background: rgba(148, 58, 50, 0.08);
    border: 1px solid rgba(148, 58, 50, 0.15);
    text-transform: uppercase;
  }

  .context-box {
    background: #f2f7f7;
    padding: 0.6rem;
    border-left: 2px solid #216273;
  }

  .context-title {
    display: block;
    font-family: "Cascadia Code", ui-monospace, monospace;
    font-size: 0.6rem;
    font-weight: bold;
    color: #477889;
    margin-bottom: 0.3rem;
    letter-spacing: 0.05em;
  }

  .context-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(80px, 1fr));
    gap: 0.4rem;
    font-size: 0.72rem;
  }

  .lbl {
    color: #7f8c8d;
    font-weight: 500;
  }

  .val {
    color: #2c3e50;
    font-weight: 600;
  }

  .context-empty {
    margin: 0;
    font-size: 0.72rem;
    color: #7f8c8d;
    font-style: italic;
  }

  .roads-list {
    margin: 0;
    padding: 0;
    list-style: none;
    font-size: 0.72rem;
    display: grid;
    gap: 0.25rem;
  }

  .roads-list li {
    display: flex;
    justify-content: space-between;
  }

  .road-name {
    color: #2c3e50;
    font-weight: 500;
  }

  .road-status {
    font-weight: 600;
    color: #27ae60;
  }

  .road-status.closed {
    color: #c0392b;
  }

  .more-roads {
    color: #7f8c8d;
    font-style: italic;
    text-align: right;
  }

  .card-form {
    display: grid;
    gap: 0.5rem;
  }

  .card-form label {
    font-size: 0.72rem;
    font-weight: 700;
    color: #34495e;
  }

  .card-form textarea, .card-form select, .card-form input {
    font-size: 0.78rem;
    padding: 0.45rem 0.5rem;
    border: 1px solid #bdc3c7;
    background: #fafbfc;
  }

  .card-form textarea:focus, .card-form select:focus, .card-form input:focus {
    border-color: #c9872f;
    outline: none;
  }

  .action-btn {
    width: 100%;
    padding: 0.55rem;
    color: #f7fbfa;
    background: #173342;
    border: 1px solid #173342;
    font-weight: 700;
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.02em;
    transition: background 0.2s;
  }

  .action-btn:hover:not(:disabled) {
    background: #216273;
    border-color: #216273;
  }

  .result-box {
    margin-top: 0.5rem;
    padding: 0.6rem;
    font-size: 0.72rem;
    border: 1px solid;
    display: grid;
    gap: 0.25rem;
  }

  .result-box.success {
    background: #ebf5fb;
    border-color: #aed6f1;
    color: #2c3e50;
  }

  .result-box.failure {
    background: #fdf2f2;
    border-color: #f8d7da;
    color: #721c24;
  }

  .res-row {
    display: flex;
    justify-content: space-between;
    border-bottom: 1px dashed rgba(0,0,0,0.06);
    padding-bottom: 0.15rem;
  }

  .res-row strong {
    font-family: "Cascadia Code", ui-monospace, monospace;
    font-size: 0.65rem;
    color: #7f8c8d;
  }

  .res-msg {
    margin: 0.2rem 0 0;
    line-height: 1.4;
    font-style: italic;
    color: #566573;
  }

  .decision-panel {
    border-top: 2px solid #bdc3c7;
    padding-top: 1rem;
    display: grid;
    gap: 0.8rem;
  }

  .input-grid {
    display: grid;
    grid-template-columns: 1fr 1.2fr;
    gap: 0.5rem;
  }

  .btn-group {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.5rem;
    margin-top: 0.3rem;
  }

  .decision-btn {
    padding: 0.55rem;
    font-weight: 700;
    font-size: 0.72rem;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    border: 1px solid;
    transition: all 0.2s;
  }

  .decision-btn span {
    font-family: "Cascadia Code", ui-monospace, monospace;
    font-size: 0.55rem;
    font-weight: normal;
    opacity: 0.85;
    margin-top: 0.1rem;
  }

  .approve-btn {
    color: #fff;
    background: #216273;
    border-color: #216273;
  }

  .approve-btn:hover:not(:disabled) {
    background: #173342;
    border-color: #173342;
  }

  .annotate-btn {
    color: #173342;
    background: #edf3f2;
    border-color: #bdc3c7;
  }

  .annotate-btn:hover:not(:disabled) {
    background: #bdc3c7;
  }

  .capitalize {
    text-transform: capitalize;
  }
</style>
