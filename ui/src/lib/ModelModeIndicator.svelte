<script>
  import HelpTip from './HelpTip.svelte';

  let {
    providers
  } = $props();

  let terraProvider = $derived(providers?.terra || 'fixture');
  let solProvider = $derived(providers?.sol || 'fixture');
  let lunaProvider = $derived(providers?.luna || 'fixture');

  function modeLabel(mode) {
    return mode === 'live' ? 'AI on' : 'Demo pack';
  }

  function modeTip(agent, mode) {
    if (agent === 'luna') {
      return mode === 'live'
        ? 'Luna (event reader) will call OpenAI when used. Still practice-only — no real feeds.'
        : 'Luna uses the pre-built demo pack (no OpenAI call). Fine for offline walkthroughs.';
    }
    if (agent === 'terra') {
      return mode === 'live'
        ? 'Terra (situation assessor) can call OpenAI when you refresh analysis. Suggestions never change the board by themselves.'
        : 'Terra shows pre-built demo assessments for this scenario (no OpenAI call).';
    }
    return mode === 'live'
      ? 'Sol (briefing helper) can call OpenAI only when you ask for a briefing. Never auto-sends.'
      : 'Sol uses the pre-built demo recommendation text (no OpenAI call).';
  }
</script>

<div class="model-modes-container" aria-label="AI mode for this demo">
  <div class="mode-indicator" data-agent="luna">
    <span class="agent-label">Luna · events</span>
    <span class="mode-badge" data-mode={lunaProvider}>
      {modeLabel(lunaProvider)}
    </span>
    <HelpTip text={modeTip('luna', lunaProvider)} label="About Luna" />
  </div>
  <div class="mode-indicator" data-agent="terra">
    <span class="agent-label">Terra · assess</span>
    <span class="mode-badge" data-mode={terraProvider}>
      {modeLabel(terraProvider)}
    </span>
    <HelpTip text={modeTip('terra', terraProvider)} label="About Terra" />
  </div>
  <div class="mode-indicator" data-agent="sol">
    <span class="agent-label">Sol · brief</span>
    <span class="mode-badge" data-mode={solProvider}>
      {modeLabel(solProvider)}
    </span>
    <HelpTip text={modeTip('sol', solProvider)} label="About Sol" />
  </div>
</div>
