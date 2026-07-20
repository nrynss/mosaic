<script>
  import HelpTip from './HelpTip.svelte';

  let {
    session = $bindable(),
    elapsedSeconds = $bindable(),
    readEnvelope,
    actionState = $bindable(),
    actionMessage = $bindable()
  } = $props();

  let errorMsg = $state('');
  let isSubmitting = $state(false);

  let statusLabel = $derived(
    session?.status === 'running'
      ? 'Playing'
      : session?.status === 'ended'
        ? 'Finished'
        : session?.status === 'paused'
          ? 'Paused'
          : 'Ready'
  );

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
    actionMessage = 'Starting the domestic-disturbance scenario…';
    elapsedSeconds = 0;
    try {
      const res = await readEnvelope('simulation/start', { method: 'POST' });
      session = res;
      actionState = 'ready';
      actionMessage = `Scenario playing (session ${res.session_id})`;
    } catch (e) {
      errorMsg = e.message;
      actionState = 'error';
      actionMessage = `Could not start: ${e.message}`;
    } finally {
      isSubmitting = false;
    }
  }

  async function resetSimulation() {
    isSubmitting = true;
    errorMsg = '';
    actionState = 'loading';
    actionMessage = 'Resetting for a fresh run…';
    elapsedSeconds = 0;
    try {
      const res = await readEnvelope('simulation/reset', { method: 'POST' });
      session = res;
      actionState = 'ready';
      actionMessage = `Ready for a new run (session ${res.session_id})`;
    } catch (e) {
      errorMsg = e.message;
      actionState = 'error';
      actionMessage = `Could not reset: ${e.message}`;
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
    <span class="status-label">
      Scenario: <strong>{statusLabel}</strong>
      <HelpTip text="Plays the 10-step synthetic domestic-disturbance call (911 → weather → road issues → correction). Practice only." label="About the scenario player" />
    </span>
    {#if session?.status === 'running'}
      <span class="timer-display">Demo clock: <strong>{formatTime(elapsedSeconds)}</strong></span>
    {/if}
  </div>

  <div class="actions">
    <button class="primary-button" onclick={startSimulation} disabled={isSubmitting || session?.status === 'running'}>
      Play scenario
      <HelpTip text="Replays the made-up call step by step so the incident board fills in. Safe to run as often as you like." label="About Play scenario" />
    </button>
    <button class="secondary-button" onclick={resetSimulation} disabled={isSubmitting}>
      Start over
      <HelpTip text="Clears the live play-through view so you can demo again. Notes you already saved stay in the history tab (on local Docker they also survive restarts)." label="About Start over" />
    </button>
  </div>

  {#if errorMsg}
    <div class="error-display">{errorMsg}</div>
  {/if}
</div>
