<script>
  let {
    cop,
    advisories,
    onPrefillMaintenance
  } = $props();

  let activeIncident = $derived(arrayOf(cop?.cop?.incidents || cop?.incidents)[0]);
  let incidentLocation = $derived(activeIncident?.location_id || '');
  let auditRecords = $derived(arrayOf(advisories?.audit_records));

  // Determine if there are matching prior road-condition or maintenance handoff records
  let matchedRecords = $derived(findPriorHandoffs(auditRecords, incidentLocation));
  let hasRecurrence = $derived(matchedRecords.length > 0);

  let preparedNoteText = $derived(
    hasRecurrence
      ? `A prior road-condition handoff exists for this area. A new maintenance note has been prepared for review. Prior records: ${matchedRecords.map(r => r.audit_record_id).join(', ')}`
      : ''
  );

  function arrayOf(value) {
    return Array.isArray(value) ? value : [];
  }

  function findPriorHandoffs(records, location) {
    if (!location) return [];
    const locLower = String(location).toLowerCase();
    
    return records.filter(record => {
      const action = String(record.action || '').toLowerCase();
      const note = String(record.note || '').toLowerCase();
      const targetId = String(record.target_id || '').toLowerCase();
      
      // Check if it's a road-condition/maintenance handoff
      const isMaint = action.includes('handoff') || action.includes('maintenance') || 
                      note.includes('handoff') || note.includes('maintenance') || 
                      note.includes('road-condition') || note.includes('road condition');
      
      if (!isMaint) return false;

      // Check if the record note or target references the current location
      const matchesLoc = note.includes(locLower) || targetId.includes(locLower);
      return matchesLoc;
    });
  }

  function doPrefill() {
    if (onPrefillMaintenance && preparedNoteText) {
      onPrefillMaintenance(preparedNoteText);
    }
  }
</script>

{#if hasRecurrence}
  <div class="recurrence-alert-banner" role="alert">
    <div class="recurrence-header">
      <span class="alert-icon">⚠️</span>
      <strong>Deterministic Recurrence Alert</strong>
      <span class="maint-badge">recorded, not sent</span>
    </div>
    <div class="recurrence-body">
      <p>A prior road-condition handoff exists for area <code>{incidentLocation}</code>. A new maintenance note has been prepared for review.</p>
      <div class="prior-records">
        <span>Prior Records:</span>
        {#each matchedRecords as record}
          <span class="prior-badge"><code>{record.audit_record_id}</code></span>
        {/each}
      </div>
      <div class="prefill-action-bar">
        <button class="prefill-maint-btn" onclick={doPrefill}>
          Pre-fill Maintenance Handoff Card
        </button>
      </div>
    </div>
    <div class="recurrence-footer">
      No autonomous external contact has been made.
    </div>
  </div>
{/if}
