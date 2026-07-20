<script>
  /**
   * Full-screen help drawer: architecture, demo walkthrough, agents, safety.
   * @type {{ open?: boolean, onClose?: () => void }}
   */
  let { open = false, onClose = () => {} } = $props();

  function onKeydown(event) {
    if (event.key === 'Escape' && open) {
      onClose();
    }
  }

  /** @type {{ id: string, title: string, body: string }[]} */
  const sections = [
    {
      id: 'what',
      title: 'What Mosaic is',
      body: `Mosaic is an auditable event-to-state foundation for operator decision-support. This demo uses a fully synthetic domestic-disturbance scenario. It is not a police product claim and never contacts real departments or external systems.`
    },
    {
      id: 'data',
      title: 'Synthetic data coverage',
      body: `The domestic-disturbance fixture includes 10 timed beats: 911 intake, welfare check, weather alert, main-road closure, EMS availability, officer update, incomplete road repair, quarantined invalid input, late EMS delivery, and a road-opening correction. That arc drives COP state through revision 9, historical Terra/Sol advisories (later superseded), and sample audit records. It is enough for a full operator walkthrough without more data generation.`
    },
    {
      id: 'walk',
      title: 'Suggested walkthrough',
      body: `1) Confirm connection is Live and agent badges (Luna/Terra/Sol). 2) Start Simulation — beats replay on a virtual clock. 3) Watch the COP facts update (incident, unit, roads, weather, resources). 4) Analyze Incident — refresh advisory history; with live providers and a funded key, operator analyze/brief endpoints call OpenAI. 5) Use Dispatch / Maintenance handoff cards — records are executed:false and delivered:false. 6) Open Provenance & Action Trail for the full audit trail. 7) Reset for a clean session.`
    },
    {
      id: 'agents',
      title: 'Luna, Terra, and Sol',
      body: `Luna normalises source events. Terra produces derived assessments (Analyze). Sol drafts recipient briefings. Each agent is fixture or live. Live requires MOSAIC_*_PROVIDER=live and a server-side OPENAI_API_KEY. Missing key falls back to fixture. A zero-balance key still shows live; failed calls are recorded as model runs and do not mutate the COP.`
    },
    {
      id: 'cop',
      title: 'COP and evidence',
      body: `The Common Operating Picture is the only operational projection. It is produced by a deterministic projector from canonical events. Click Resolve evidence on any claim to open the bounded evidence artifact (raw payload bytes withheld). Models may assess the COP but cannot change it.`
    },
    {
      id: 'safety',
      title: 'Safety boundary',
      body: `Every operator action is an immutable audit record with executed:false. Handoffs are not delivered externally (delivered:false). There is no login; the public demo actor is open for review. Synthetic data only — no real PII or operational feeds.`
    },
    {
      id: 'persist',
      title: 'Persistence',
      body: `Local Docker uses a named volume so SQLite audits survive container restarts. Cloud Run uses /tmp (ephemeral): cold starts reseed the fixture and drop prior operator history. Litestream/Cloud SQL are future durable options, not the live hackathon deploy.`
    },
    {
      id: 'tabs',
      title: 'Workspace tabs',
      body: `Incident Command Workspace is the live operator surface (sim, COP, advisories, recurrence). Provenance & Action Trail lists model runs, audits, and session beats for the decision trail. The right rail holds evidence resolution and handoff/review forms. The bottom drawer is developer status (health, version, operations, API base).`
    }
  ];

  let activeId = $state('what');
  let active = $derived(sections.find((s) => s.id === activeId) || sections[0]);
</script>

<svelte:window onkeydown={onKeydown} />

{#if open}
  <div
    class="help-overlay"
    role="presentation"
    onclick={onClose}
    onkeydown={(e) => {
      if (e.key === 'Enter' || e.key === ' ') onClose();
    }}
  >
    <div
      class="help-panel"
      role="dialog"
      aria-modal="true"
      aria-labelledby="help-panel-title"
      tabindex="-1"
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => e.stopPropagation()}
    >
      <header class="help-panel-header">
        <div>
          <p class="eyebrow">Online help</p>
          <h2 id="help-panel-title">How this demo works</h2>
        </div>
        <button type="button" class="help-close" onclick={onClose} aria-label="Close help">
          Close
        </button>
      </header>

      <div class="help-panel-body">
        <nav class="help-toc" aria-label="Help topics">
          {#each sections as section (section.id)}
            <button
              type="button"
              class="help-toc-item"
              class:active={activeId === section.id}
              onclick={() => (activeId = section.id)}
            >
              {section.title}
            </button>
          {/each}
        </nav>
        <article class="help-content">
          <h3>{active.title}</h3>
          <p>{active.body}</p>
          {#if activeId === 'walk'}
            <ol class="help-steps">
              <li>Press <strong>Play scenario</strong> and watch the synthetic call unfold.</li>
              <li>Read facts on the board; click <strong>Show source</strong> when curious.</li>
              <li>Refresh advice — older access warnings may become out of date after the road reopens.</li>
              <li>Save a Dispatch or maintenance note (demo only — not sent).</li>
              <li>Open <strong>Decision history</strong> for the paper trail.</li>
            </ol>
          {/if}
          {#if activeId === 'data'}
            <ul class="help-list">
              <li>10 raw events / simulation beats</li>
              <li>9 projected COP state revisions at completion</li>
              <li>Incident, unit, EMS resource, two roads, weather alert</li>
              <li>Quarantine path + late-delivery path for integrity demos</li>
              <li>Fixture Terra insights + Sol recommendation (historical)</li>
              <li>Sample supervisor audit actions</li>
            </ul>
          {/if}
        </article>
      </div>

      <footer class="help-panel-footer">
        <p>
          Tip: hover the <span class="help-tip-glyph inline" aria-hidden="true">?</span> marks next to
          controls for short field tips. Press <kbd>Esc</kbd> to close this panel.
        </p>
      </footer>
    </div>
  </div>
{/if}
