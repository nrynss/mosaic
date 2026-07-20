<script>
  let {
    providers
  } = $props();

  let terraProvider = $derived(providers?.terra || 'fixture');
  let solProvider = $derived(providers?.sol || 'fixture');
  let lunaProvider = $derived(providers?.luna || 'fixture');
</script>

<div class="model-modes-container" aria-label="AI capability mode indicators">
  <div class="mode-indicator" data-agent="luna">
    <span class="agent-label">Luna</span>
    <span class="mode-badge" data-mode={lunaProvider}>
      {lunaProvider}
    </span>
  </div>
  <div class="mode-indicator" data-agent="terra">
    <span class="agent-label">Terra</span>
    <span class="mode-badge" data-mode={terraProvider}>
      {terraProvider}
    </span>
  </div>
  <div class="mode-indicator" data-agent="sol">
    <span class="agent-label">Sol</span>
    <span class="mode-badge" data-mode={solProvider}>
      {solProvider}
    </span>
  </div>
</div>
