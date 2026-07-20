<script>
  import HelpTip from './HelpTip.svelte';

  let {
    readEnvelope,
    cop,
    advisories,
    selectEvidence,
    auditTargetID = $bindable(''),
    auditTargetKind = $bindable('recommendation'),
    actionState = $bindable('idle'),
    actionMessage = $bindable(''),
    maintenanceNote = $bindable('')
  } = $props();

  // Local state for Dispatch Handoff
  let dispatchObservations = $state('');
  let dispatchResult = $state(null);
  let dispatchError = $state('');

  // Local state for Maintenance Handoff
  let maintenanceResult = $state(null);
  let maintenanceError = $state('');

  // Local state for Operator Decision Controls
  let decisionNote = $state('');
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
    <span class="eyebrow">
      Practice notes for other teams
      <HelpTip text="Practice drafting a briefing to Dispatch or Maintenance. This mirrors the real handoff form so you can rehearse the workflow." label="About practice handoffs" />
    </span>
    <h3>Draft a handoff (not sent)</h3>
    <p class="section-desc">
      These forms look like inter-team handoffs so you can practice the workflow.
      Every save is marked <strong>not carried out</strong> and <strong>not delivered</strong>.
    </p>
  </div>

  <!-- Dispatch Card -->
  <div class="action-card dispatch-card">
    <div class="card-header">
      <span class="badge recipient-badge">For: Dispatch desk</span>
      <span class="badge status-badge safety-badge">
        saved only · not sent
        <HelpTip text="Honest label: your note is stored for the demo trail. No real dispatcher receives it." label="About not sent" />
      </span>
    </div>

    <div class="context-box">
      <span class="context-title">This call (from the board)</span>
      {#if activeIncident}
        <div class="context-grid">
          <div><span class="lbl">Call:</span> <code class="val">{activeIncident.incident_id}</code></div>
          <div><span class="lbl">Type:</span> <span class="val">{activeIncident.category || '—'}</span></div>
          <div><span class="lbl">Where:</span> <span class="val">{activeIncident.location_id || '—'}</span></div>
        </div>
      {:else}
        <p class="context-empty">Play the scenario so a call appears on the board.</p>
      {/if}
    </div>

    <form onsubmit={handleDispatchHandoff} class="card-form">
      <label for="dispatch-observations">Your note to Dispatch</label>
      <textarea
        id="dispatch-observations"
        bind:value={dispatchObservations}
        rows="3"
        placeholder="e.g. Unit on scene; Brook Lane access may be constrained by weather…"
      ></textarea>
      <button class="action-btn" type="submit" disabled={actionState === 'loading'}>
        Save Dispatch note
      </button>
    </form>

    {#if dispatchResult}
      <div class="result-box success">
        <p class="res-msg">Saved to the demo log. Nothing was sent.</p>
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
      <span class="badge recipient-badge">For: Road / maintenance</span>
      <span class="badge status-badge safety-badge">saved only · not sent</span>
    </div>

    <div class="context-box">
      <span class="context-title">Roads on the board</span>
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
        <p class="context-empty">No roads on the board yet — play the scenario.</p>
      {/if}
    </div>

    <form onsubmit={handleMaintenanceHandoff} class="card-form">
      <label for="maintenance-note">Your note about roads / access</label>
      <textarea
        id="maintenance-note"
        bind:value={maintenanceNote}
        rows="3"
        placeholder="e.g. Brook Lane closed during heavy rain; flag for maintenance review…"
      ></textarea>
      <button class="action-btn" type="submit" disabled={actionState === 'loading'}>
        Save maintenance note
      </button>
    </form>

    {#if maintenanceResult}
      <div class="result-box success">
        <p class="res-msg">Saved to the demo log. Nothing was sent.</p>
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
      <span class="eyebrow">
        Your call as operator
        <HelpTip text="Agree with advice or add a note. Saved to the demo history only — never an operational command." label="About your decision" />
      </span>
      <h3>Record a decision</h3>
      <p class="section-desc">
        Pick what you are reviewing (use “Use in my decision” on an advice card to fill this in).
        Nothing here becomes a live dispatch.
      </p>
    </div>

    <div class="decision-form card-form">
      <div class="input-grid">
        <div>
          <label for="decision-kind">What are you reviewing?</label>
          <select id="decision-kind" bind:value={auditTargetKind}>
            <option value="recommendation">Recommendation</option>
            <option value="insight">Assessment</option>
            <option value="briefing">Briefing</option>
            <option value="system">System / other</option>
          </select>
        </div>
        <div>
          <label for="decision-target">Which record? (id)</label>
          <input
            id="decision-target"
            bind:value={auditTargetID}
            placeholder="e.g. recommendation-domestic-001"
            required
          />
        </div>
      </div>

      <label for="decision-note">Your note</label>
      <textarea
        id="decision-note"
        bind:value={decisionNote}
        rows="3"
        placeholder="e.g. Agree access risk is outdated after road reopened…"
      ></textarea>

      <div class="btn-group">
        <button
          class="decision-btn approve-btn"
          type="button"
          onclick={() => handleDecision('approve')}
          disabled={actionState === 'loading'}
        >
          Agree / approve
        </button>
        <button
          class="decision-btn annotate-btn"
          type="button"
          onclick={() => handleDecision('annotate')}
          disabled={actionState === 'loading'}
        >
          Add note only
        </button>
      </div>
    </div>

    {#if decisionResult}
      <div class="result-box success">
        <div class="res-row"><strong>What you did:</strong> <span class="capitalize">{decisionResult.action}</span></div>
        <p class="res-msg">Saved to the demo log. Nothing was sent.</p>
        <div class="res-row"><strong>History id:</strong> <code>{decisionResult.audit_record_id}</code></div>
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
    gap: 1rem;
    padding-top: 0.75rem;
  }

  .panel-section-header {
    border-bottom: 1px solid var(--line-strong);
    padding-bottom: 0.4rem;
    margin-bottom: 0.35rem;
  }

  .panel-section-header h3 {
    font-size: 0.85rem;
    color: var(--ink);
    margin: 0.2rem 0;
  }

  .section-desc {
    font-size: 0.68rem;
    color: var(--ink-dim);
    margin: 0;
    line-height: 1.45;
  }

  .action-card {
    background: var(--bg0);
    border: 1px solid var(--line);
    border-left: 3px solid var(--amber);
    padding: 0.7rem;
    display: grid;
    gap: 0.6rem;
  }

  .card-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 0.4rem;
    border-bottom: 1px solid var(--line);
    padding-bottom: 0.4rem;
  }

  .badge {
    font-family: var(--mono);
    font-size: 0.56rem;
    font-weight: bold;
    padding: 0.1rem 0.35rem;
    letter-spacing: 0.05em;
  }

  .recipient-badge {
    color: var(--ink-dim);
    border: 1px solid var(--line-strong);
    text-transform: uppercase;
  }

  .status-badge.safety-badge {
    color: var(--alert);
    border: 1px solid var(--alert);
    text-transform: uppercase;
    display: inline-flex;
    align-items: center;
    gap: 0.2rem;
  }

  .context-box {
    background: var(--bg2);
    padding: 0.5rem;
    border-left: 2px solid var(--info);
  }

  .context-title {
    display: block;
    font-family: var(--mono);
    font-size: 0.54rem;
    font-weight: bold;
    color: var(--ink-faint);
    margin-bottom: 0.3rem;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }

  .context-grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(80px, 1fr));
    gap: 0.35rem;
    font-size: 0.64rem;
    font-family: var(--mono);
  }

  .lbl {
    color: var(--ink-faint);
    font-weight: 500;
  }

  .val {
    color: var(--ink);
    font-weight: 600;
  }

  .context-empty {
    margin: 0;
    font-size: 0.64rem;
    color: var(--ink-faint);
    font-family: var(--mono);
  }

  .roads-list {
    margin: 0;
    padding: 0;
    list-style: none;
    font-size: 0.64rem;
    font-family: var(--mono);
    display: grid;
    gap: 0.2rem;
  }

  .roads-list li {
    display: flex;
    justify-content: space-between;
    gap: 0.5rem;
  }

  .road-name {
    color: var(--ink);
    font-weight: 500;
  }

  .road-status {
    font-weight: 700;
    color: var(--ok);
    text-transform: uppercase;
    font-size: 0.58rem;
  }

  .road-status.closed {
    color: var(--alert);
  }

  .more-roads {
    color: var(--ink-faint);
    text-align: right;
  }

  .card-form {
    display: grid;
    gap: 0.4rem;
  }

  .card-form label {
    font-size: 0.6rem;
    font-weight: 700;
    color: var(--ink-dim);
  }

  .card-form textarea, .card-form select, .card-form input {
    font-size: 0.7rem;
    padding: 0.4rem 0.45rem;
    border: 1px solid var(--line-strong);
    background: var(--bg1);
    color: var(--ink);
  }

  .card-form textarea:focus, .card-form select:focus, .card-form input:focus {
    border-color: var(--amber);
    outline: none;
  }

  .action-btn {
    width: 100%;
    padding: 0.45rem;
    color: var(--ink);
    background: transparent;
    border: 1px solid var(--line-strong);
    font-family: var(--mono);
    font-weight: 700;
    font-size: 0.62rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
  }

  .action-btn:hover:not(:disabled) {
    border-color: var(--amber);
    color: var(--amber);
  }

  .result-box {
    margin-top: 0.35rem;
    padding: 0.5rem;
    font-size: 0.64rem;
    border: 1px solid;
    display: grid;
    gap: 0.25rem;
  }

  .result-box.success {
    background: rgba(67, 193, 104, 0.06);
    border-color: rgba(67, 193, 104, 0.4);
    color: var(--ink-dim);
  }

  .result-box.failure {
    background: rgba(255, 93, 82, 0.06);
    border-color: rgba(255, 93, 82, 0.4);
    color: var(--alert);
  }

  .res-row {
    display: flex;
    justify-content: space-between;
    gap: 0.5rem;
    border-bottom: 1px dashed var(--line);
    padding-bottom: 0.15rem;
  }

  .res-row strong {
    font-family: var(--mono);
    font-size: 0.56rem;
    color: var(--ink-faint);
    text-transform: uppercase;
  }

  .res-msg {
    margin: 0.15rem 0 0;
    line-height: 1.4;
    color: var(--ink-dim);
  }

  .decision-panel {
    border-top: 1px solid var(--line-strong);
    padding-top: 0.75rem;
    display: grid;
    gap: 0.6rem;
  }

  .input-grid {
    display: grid;
    grid-template-columns: 1fr 1.2fr;
    gap: 0.4rem;
  }

  .btn-group {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 0.4rem;
    margin-top: 0.25rem;
  }

  .decision-btn {
    padding: 0.45rem;
    font-family: var(--mono);
    font-weight: 700;
    font-size: 0.62rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    display: flex;
    align-items: center;
    justify-content: center;
    border: 1px solid;
  }

  .approve-btn {
    color: var(--ok);
    background: transparent;
    border-color: var(--ok);
  }

  .approve-btn:hover:not(:disabled) {
    background: rgba(67, 193, 104, 0.12);
  }

  .annotate-btn {
    color: var(--ink-dim);
    background: transparent;
    border-color: var(--line-strong);
  }

  .annotate-btn:hover:not(:disabled) {
    color: var(--ink);
    border-color: var(--ink-dim);
  }

  .capitalize {
    text-transform: capitalize;
  }
</style>
