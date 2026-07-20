<script>
  /**
   * Inline hover/focus tip. Use beside labels or controls.
   * @type {{ text: string, label?: string }}
   */
  let { text, label = 'More information' } = $props();
</script>

<button type="button" class="help-tip" aria-label={label}>
  <span class="help-tip-glyph" aria-hidden="true">?</span>
  <span class="help-tip-bubble" role="tooltip">{text}</span>
</button>
