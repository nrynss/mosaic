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
      body: `Mosaic shows an operator one trustworthy picture of an unfolding incident, with AI advice on the side that can never change the facts.`
    },
    {
      id: 'data',
      title: 'Synthetic data coverage',
      body: `The domestic-disturbance fixture includes 10 timed beats: 911 intake, welfare check, weather alert, main-road closure, EMS availability, officer update, incomplete road repair, quarantined invalid input, late EMS delivery, and a road-opening correction. That arc drives the board state through revision 9, historical Terra/Sol advisories, and sample audit records. It is enough for a full operator walkthrough without more data generation.`
    },
    {
      id: 'walk',
      title: 'Suggested walkthrough',
      body: `Five steps for a first run:`
    },
    {
      id: 'cop',
      title: 'Board state and evidence',
      body: `The Live incident board is the single source of truth. It is generated automatically from raw inputs by a deterministic projection engine. You can click "Show source" on any claim to inspect the exact input event. AI models can give advice on the side, but they can never change the facts.`
    },
    {
      id: 'safety',
      title: 'Safety boundary',
      body: `Every action you take is written to a permanent, read-only log — but nothing is actually carried out and no handoff leaves the demo. There is no login; this public demo actor is open for review. Synthetic data only — no real PII or operational feeds.`
    },
    {
      id: 'tabs',
      title: 'Workspace tabs',
      body: `"Live incident board" is the live operator surface (scenario controls, active incident details, timeline, and advice). "Decision history" lists model runs, audits, and scenario beats for the decision trail. The right rail holds evidence resolution and handoff/review forms. The bottom drawer is the developer console (health, version, operations, API base).`
    },
    {
      id: 'dev',
      title: 'For developers',
      body: `Live models require a server-side OPENAI_API_KEY. Missing keys fall back to pre-built fixtures. Docker deployments use a named volume for SQLite database persistence, while Cloud Run uses /tmp (ephemeral) and resets on restart. For a durable deployment you would add Litestream or Cloud SQL.`
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
