<script>
  /**
   * Inline hover/focus/tap tip. Use beside labels or controls.
   * @type {{ text: string, label?: string, direction?: 'top' | 'bottom', align?: 'start' | 'end' }}
   */
  let { text, label = 'More information', direction = 'top', align = 'start' } = $props();

  // Tap-to-open support for touch devices where :focus-within does not
  // reliably fire on button tap (notably iOS Safari).
  let open = $state(false);
  let wrapper = $state(undefined);

  let bubbleId = $derived(
    'help-tip-' +
      label
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/(^-+|-+$)/g, '') +
      '-bubble'
  );

  function toggleOpen() {
    open = !open;
  }

  function onWindowClick(event) {
    if (open && wrapper && !wrapper.contains(event.target)) {
      open = false;
    }
  }

  function onKeydown(event) {
    if (event.key === 'Escape') {
      open = false;
      event.currentTarget.blur();
    }
  }
</script>

<svelte:window onclick={onWindowClick} />

<span class="help-tip" data-direction={direction} data-align={align} class:open bind:this={wrapper}>
  <button
    type="button"
    class="help-tip-btn"
    aria-label={label}
    aria-describedby={bubbleId}
    onclick={toggleOpen}
    onkeydown={onKeydown}
  >
    <span class="help-tip-glyph" aria-hidden="true">?</span>
  </button>
  <span id={bubbleId} class="help-tip-bubble" role="tooltip">{text}</span>
</span>
