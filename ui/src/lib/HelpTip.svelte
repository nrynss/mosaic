<script>
  /**
   * Inline hover/focus/tap tip. Use beside labels or controls.
   * The bubble is fixed-positioned and clamped to the viewport at show
   * time, so it never inherits a narrow containing block (which rendered
   * text vertically) and never clips against overflow ancestors.
   * @type {{ text: string, label?: string, direction?: 'top' | 'bottom', align?: 'start' | 'end' }}
   */
  let { text, label = 'More information', direction = 'top', align = 'start' } = $props();

  // Tap-to-open support for touch devices where :focus-within does not
  // reliably fire on button tap (notably iOS Safari).
  let open = $state(false);
  let wrapper = $state(undefined);
  let bubble = $state(undefined);
  let button = $state(undefined);
  let bubbleStyle = $state('');

  let bubbleId = $derived(
    'help-tip-' +
      label
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, '-')
        .replace(/(^-+|-+$)/g, '') +
      '-bubble'
  );

  // The bubble keeps layout while hidden (visibility: hidden), so its
  // size is measurable before it becomes visible.
  function place() {
    if (!button || !bubble) return;
    const anchor = button.getBoundingClientRect();
    const width = bubble.offsetWidth;
    const height = bubble.offsetHeight;
    const margin = 8;
    const gap = 6;

    let top = direction === 'bottom' ? anchor.bottom + gap : anchor.top - height - gap;
    if (top < margin) top = anchor.bottom + gap;
    if (top + height > window.innerHeight - margin) top = anchor.top - height - gap;

    let left = anchor.left + anchor.width / 2 - width / 2;
    left = Math.max(margin, Math.min(left, window.innerWidth - width - margin));

    bubbleStyle = `top:${Math.round(top)}px;left:${Math.round(left)}px;`;
  }

  function toggleOpen() {
    open = !open;
    if (open) place();
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

<svelte:window onclick={onWindowClick} onresize={() => open && place()} />

<span class="help-tip" data-direction={direction} data-align={align} class:open bind:this={wrapper}>
  <button
    type="button"
    class="help-tip-btn"
    aria-label={label}
    aria-describedby={bubbleId}
    bind:this={button}
    onclick={toggleOpen}
    onkeydown={onKeydown}
    onmouseenter={place}
    onfocus={place}
  >
    <span class="help-tip-glyph" aria-hidden="true">?</span>
  </button>
  <span id={bubbleId} class="help-tip-bubble" role="tooltip" bind:this={bubble} style={bubbleStyle}>{text}</span>
</span>
