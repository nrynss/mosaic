<script>
  let {
    session = $bindable(),
    elapsedSeconds = $bindable(),
    readEnvelope,
    actionState = $bindable(),
    actionMessage = $bindable()
  } = $props();

  let errorMsg = $state('');
  let isSubmitting = $state(false);

  let timerId;
  $effect(() => {
    if (session?.status === 'running') {
      if (!timerId) {
        timerId = setInterval(() => {
          elapsedSeconds += 1;
        }, 1000);
      }
    } else {
      if (timerId) {
        clearInterval(timerId);
        timerId = null;
      }
    }
    return () => {
      if (timerId) {
        clearInterval(timerId);
        timerId = null;
      }
    };
  });

  async function startSimulation() {
    isSubmitting = true;
    errorMsg = '';
    actionState = 'loading';
    actionMessage = 'Starting simulation...';
    elapsedSeconds = 0;
    try {
      const res = await readEnvelope('simulation/start', { method: 'POST' });
      session = res;
      actionState = 'ready';
      actionMessage = `Simulation started (Session: ${res.session_id})`;
    } catch (e) {
      errorMsg = e.message;
      actionState = 'error';
      actionMessage = `Start failed: ${e.message}`;
    } finally {
      isSubmitting = false;
    }
  }

  async function resetSimulation() {
    isSubmitting = true;
    errorMsg = '';
    actionState = 'loading';
    actionMessage = 'Resetting simulation...';
    elapsedSeconds = 0;
    try {
      const res = await readEnvelope('simulation/reset', { method: 'POST' });
      session = res;
      actionState = 'ready';
      actionMessage = `Simulation reset to fresh session: ${res.session_id}`;
    } catch (e) {
      errorMsg = e.message;
      actionState = 'error';
      actionMessage = `Reset failed: ${e.message}`;
    } finally {
      isSubmitting = false;
    }
  }

  function formatTime(total) {
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const remainder = total % 60;
    if (hours > 0) return `${hours}h ${minutes}m ${remainder}s`;
    if (minutes > 0) return `${minutes}m ${remainder}s`;
    return `${remainder}s`;
  }
</script>

<div class="simulation-controls-bar">
  <div class="status-indicator">
    <span class="status-dot" class:running={session?.status === 'running'} class:ended={session?.status === 'ended'}></span>
    <span class="status-label">Simulation: <strong>{session?.status || 'inactive'}</strong></span>
    {#if session?.status === 'running'}
      <span class="timer-display">Elapsed: <strong>{formatTime(elapsedSeconds)}</strong></span>
    {/if}
  </div>

  <div class="actions">
    <button class="primary-button" onclick={startSimulation} disabled={isSubmitting || session?.status === 'running'}>
      Start Simulation
    </button>
    <button class="secondary-button" onclick={resetSimulation} disabled={isSubmitting}>
      Reset
    </button>
  </div>

  {#if errorMsg}
    <div class="error-display">{errorMsg}</div>
  {/if}
</div>
