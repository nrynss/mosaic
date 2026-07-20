<script>
  let {
    session,
    advisories,
    selectEvidence
  } = $props();

  let auditRecords = $derived(arrayOf(advisories?.audit_records));
  let modelRuns = $derived(arrayOf(advisories?.model_runs));
  let beats = $derived(arrayOf(session?.beats));

  function arrayOf(value) {
    return Array.isArray(value) ? value : [];
  }

  function formatTimestamp(value) {
    if (!value) return 'Time not recorded';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return new Intl.DateTimeFormat(undefined, {
      day: '2-digit', month: 'short', year: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', timeZoneName: 'short'
    }).format(date);
  }
</script>

<div class="provenance-container">
  <div class="provenance-header">
    <h2>Provenance & Audit Trail</h2>
    <div class="boundary-badge">
      <span class="badge-status">Recorded only · executed: false · delivered: false</span>
    </div>
  </div>

  <p class="provenance-copy">
    Every recommendation, assessment, and action displayed in Mosaic is backed by durable, immutable records. Use this trace to verify timing, inputs, and operator decisions.
  </p>

  <div class="provenance-grid">
    <!-- Chronological Audit & Decision Trail -->
    <section class="provenance-section" aria-labelledby="decisions-title">
      <h3 id="decisions-title">Operator Actions & Review Records</h3>
      {#if auditRecords.length === 0}
        <div class="empty-trace">No operator review actions have been recorded yet.</div>
      {:else}
        <ol class="timeline" aria-label="Operator actions timeline">
          {#each auditRecords as record (record.audit_record_id)}
            <li class="timeline-item">
              <div class="timeline-badge action-badge" data-action={record.action}></div>
              <div class="timeline-time">{formatTimestamp(record.created_at)}</div>
              <div class="timeline-content">
                <div class="timeline-top">
                  <strong>{record.action.replaceAll('_', ' ')}</strong>
                  <span class="actor-tag">{record.actor_role} ({record.actor_id})</span>
                </div>
                <p class="record-note">{record.note}</p>
                <div class="record-meta">
                  <span>Target: <code>{record.target_kind}:{record.target_id}</code></span>
                  <div class="record-actions">
                    <button class="small-evidence-btn" onclick={() => selectEvidence(record.target_kind, record.target_id, `${record.target_kind} · ${record.target_id}`)}>
                      Resolve evidence
                    </button>
                    <span class="safety-indicator">executed: false</span>
                  </div>
                </div>
              </div>
            </li>
          {/each}
        </ol>
      {/if}
    </section>

    <!-- AI Model Executions -->
    <section class="provenance-section" aria-labelledby="models-title">
      <h3 id="models-title">AI Model Executions (ModelRuns)</h3>
      {#if modelRuns.length === 0}
        <div class="empty-trace">No AI model invocations have occurred in this session.</div>
      {:else}
        <ol class="timeline" aria-label="Model runs timeline">
          {#each modelRuns as run (run.model_run_id)}
            <li class="timeline-item">
              <div class="timeline-badge model-badge" data-agent={run.agent}></div>
              <div class="timeline-time">{formatTimestamp(run.started_at)}</div>
              <div class="timeline-content">
                <div class="timeline-top">
                  <strong>{run.agent.toUpperCase()} Assessment</strong>
                  <span class="validation-badge" data-status={run.validation_status}>{run.validation_status}</span>
                </div>
                <div class="model-run-details">
                  <div><span class="detail-lbl">Model</span> <span class="detail-val"><code>{run.model}</code></span></div>
                  <div><span class="detail-lbl">Provider</span> <span class="detail-val"><code>{run.provider}</code></span></div>
                  <div><span class="detail-lbl">Revision</span> <span class="detail-val"><code>{run.state_revision}</code></span></div>
                </div>
                <div class="model-relations">
                  {#if run.input_event_ids?.length}
                    <div class="relation-row">
                      <span class="relation-lbl">Inputs:</span>
                      <div class="relation-tags">
                        {#each run.input_event_ids as inp}
                          <button class="tag-btn" onclick={() => selectEvidence('canonical_event', inp, `Canonical · ${inp}`)}>{inp}</button>
                        {/each}
                      </div>
                    </div>
                  {/if}
                  {#if run.output_ids?.length}
                    <div class="relation-row">
                      <span class="relation-lbl">Outputs:</span>
                      <div class="relation-tags">
                        {#each run.output_ids as out}
                          <button class="tag-btn" onclick={() => selectEvidence(run.agent === 'terra' ? 'insight' : 'recommendation', out, `${run.agent === 'terra' ? 'Insight' : 'Recommendation'} · ${out}`)}>{out}</button>
                        {/each}
                      </div>
                    </div>
                  {/if}
                </div>
              </div>
            </li>
          {/each}
        </ol>
      {/if}
    </section>

    <!-- Simulation Beats -->
    <section class="provenance-section" aria-labelledby="beats-title">
      <h3 id="beats-title">Simulation Beat & Intake Ingestion</h3>
      {#if beats.length === 0}
        <div class="empty-trace">No beats have been replayed. Start the simulation to view ingestion events.</div>
      {:else}
        <ol class="timeline" aria-label="Ingested beats timeline">
          {#each beats as beat (beat.beat_id)}
            <li class="timeline-item">
              <div class="timeline-badge beat-badge"></div>
              <div class="timeline-content">
                <div class="timeline-top">
                  <strong>Beat #{beat.order}</strong>
                  <span class="delay-tag">+{beat.delay_ms}ms</span>
                </div>
                <div class="beat-details">
                  <span>Beat ID: <code>{beat.beat_id}</code></span>
                  <span>Raw Event ID: <code>{beat.raw_event_id}</code></span>
                </div>
                <div class="beat-actions">
                  <button class="small-evidence-btn" onclick={() => selectEvidence('raw_event', beat.raw_event_id, `Raw Event · ${beat.raw_event_id}`)}>
                    Resolve raw source event
                  </button>
                </div>
              </div>
            </li>
          {/each}
        </ol>
      {/if}
    </section>
  </div>
</div>
