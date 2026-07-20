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
    <h2>Decision history</h2>
    <div class="boundary-badge">
      <span class="badge-status">Demo log only · not carried out · not delivered</span>
    </div>
  </div>

  <p class="provenance-copy">
    This tab is the paper trail for the demo: what the scenario advised, what you recorded,
    and which analysis runs ran. Use it to show auditors that every suggestion has a source —
    and that nothing left the building.
  </p>

  <div class="provenance-grid">
    <!-- Chronological Audit & Decision Trail -->
    <section class="provenance-section" aria-labelledby="decisions-title">
      <h3 id="decisions-title">What you recorded</h3>
      {#if auditRecords.length === 0}
        <div class="empty-trace">No operator notes yet — save a handoff or decision on the live board.</div>
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
                  <span>About: <code>{record.target_kind}:{record.target_id}</code></span>
                  <div class="record-actions">
                    <button class="small-evidence-btn" onclick={() => selectEvidence(record.target_kind, record.target_id, `${record.target_kind} · ${record.target_id}`)}>
                      Show source
                    </button>
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
      <h3 id="models-title">Analysis runs (Terra / Sol / Luna)</h3>
      {#if modelRuns.length === 0}
        <div class="empty-trace">No analysis runs yet for this view.</div>
      {:else}
        <ol class="timeline" aria-label="Model runs timeline">
          {#each modelRuns as run (run.model_run_id)}
            <li class="timeline-item">
              <div class="timeline-badge model-badge" data-agent={run.agent}></div>
              <div class="timeline-time">{formatTimestamp(run.started_at)}</div>
              <div class="timeline-content">
                <div class="timeline-top">
                  <strong>{run.agent === 'terra' ? 'Terra assessment' : run.agent === 'sol' ? 'Sol recommendation' : run.agent === 'luna' ? 'Luna event read' : run.agent}</strong>
                  <span class="validation-badge" data-status={run.validation_status}>{run.validation_status}</span>
                </div>
                <div class="model-run-details">
                  <div><span class="detail-lbl">Engine</span> <span class="detail-val"><code>{run.model}</code></span></div>
                  <div><span class="detail-lbl">Source</span> <span class="detail-val"><code>{run.provider}</code></span></div>
                  <div><span class="detail-lbl">Board update #</span> <span class="detail-val"><code>{run.state_revision}</code></span></div>
                </div>
                <div class="model-relations">
                  {#if run.input_event_ids?.length}
                    <div class="relation-row">
                      <span class="relation-lbl">Based on:</span>
                      <div class="relation-tags">
                        {#each run.input_event_ids as inp}
                          <button class="tag-btn" onclick={() => selectEvidence('canonical_event', inp, `Event · ${inp}`)}>{inp}</button>
                        {/each}
                      </div>
                    </div>
                  {/if}
                  {#if run.output_ids?.length}
                    <div class="relation-row">
                      <span class="relation-lbl">Produced:</span>
                      <div class="relation-tags">
                        {#each run.output_ids as out}
                          <button class="tag-btn" onclick={() => selectEvidence(run.agent === 'terra' ? 'insight' : 'recommendation', out, `${run.agent === 'terra' ? 'Assessment' : 'Recommendation'} · ${out}`)}>{out}</button>
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
      <h3 id="beats-title">Scenario steps this run</h3>
      {#if beats.length === 0}
        <div class="empty-trace">No scenario steps yet. Press <strong>Play scenario</strong> on the live board.</div>
      {:else}
        <ol class="timeline" aria-label="Scenario steps timeline">
          {#each beats as beat (beat.beat_id)}
            <li class="timeline-item">
              <div class="timeline-badge beat-badge"></div>
              <div class="timeline-content">
                <div class="timeline-top">
                  <strong>Step #{beat.order}</strong>
                  <span class="delay-tag">+{beat.delay_ms / 1000}s</span>
                </div>
                <div class="beat-details">
                  <span>Step: <code>{beat.beat_id}</code></span>
                  <span>Incoming event: <code>{beat.raw_event_id}</code></span>
                </div>
                <div class="beat-actions">
                  <button class="small-evidence-btn" onclick={() => selectEvidence('raw_event', beat.raw_event_id, `Incoming event · ${beat.raw_event_id}`)}>
                    Show incoming event
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
