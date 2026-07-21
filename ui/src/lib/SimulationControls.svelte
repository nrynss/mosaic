<script>
  import HelpTip from './HelpTip.svelte';

  let {
    session = $bindable(),
    elapsedSeconds = $bindable(),
    readEnvelope,
    actionState = $bindable(),
    actionMessage = $bindable(),
    cassetteMode = 'passthrough',
    loadAdvisories = null
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

  // Process-level cassette mode from API (set at server start via MOSAIC_SIM_MODE).
  let modeKey = $derived(normalizeCassetteMode(cassetteMode));
  let modeLabel = $derived(cassetteModeLabel(modeKey));
  let replayEnabled = $derived(modeKey === 'replay');

  function normalizeCassetteMode(raw) {
    const value = String(raw || '').trim().toLowerCase();
    if (value === 'replay' || value === 'recorded') return 'replay';
    if (value === 'record' || value === 'live') return 'record';
    return 'passthrough';
  }

  function cassetteModeLabel(key) {
    if (key === 'replay') return 'Replay';
    if (key === 'record') return 'Live (recording)';
    return 'Fixture';
  }

  function cassetteModeTip(key) {
    if (key === 'replay') {
      return 'Process started with MOSAIC_SIM_MODE=replay. Terra/Sol use banked cassette recordings — no paid OpenAI call. Set the same MOSAIC_CASSETTE_DIR as the live bank.';
    }
    if (key === 'record') {
      return 'Process started with MOSAIC_SIM_MODE=live (record). Live Terra/Sol calls are banked to MOSAIC_CASSETTE_DIR for later free replay. Restart with MOSAIC_SIM_MODE=replay to use banked advice without API cost.';
    }
    return 'Process is on the fixture/demo-pack path (MOSAIC_SIM_MODE=fixture or unset). Pre-built scenario advice only — not free cassette replay of a prior live run.';
  }

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

  async function replayLastRun() {
    if (!replayEnabled) {
      return;
    }
    isSubmitting = true;
    errorMsg = '';
    actionState = 'loading';
    actionMessage = 'Replaying banked advice (cassette mode)…';
    try {
      if (typeof loadAdvisories === 'function') {
        await loadAdvisories();
      }
      actionState = 'ready';
      actionMessage = 'Using banked cassette recordings — no paid API call for this refresh.';
    } catch (e) {
      errorMsg = e.message;
      actionState = 'error';
      actionMessage = `Could not refresh banked advice: ${e.message}`;
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
    <span class="cassette-mode-pill" data-mode={modeKey} aria-label={`Inference mode: ${modeLabel}`}>
      Mode: <strong>{modeLabel}</strong>
      <HelpTip text={cassetteModeTip(modeKey)} label="About inference mode" />
    </span>
  </div>

  <div class="actions">
    <button class="primary-button" onclick={startSimulation} disabled={isSubmitting || session?.status === 'running'}>
      Play scenario
      <HelpTip text="Replays the made-up call step by step so the incident board fills in. Safe to run as often as you like." label="About Play scenario" />
    </button>
    <button
      class="secondary-button"
      onclick={replayLastRun}
      disabled={isSubmitting || !replayEnabled}
      aria-disabled={!replayEnabled}
      title={replayEnabled
        ? 'Refresh advice using banked cassette recordings (no paid API call)'
        : 'Start the process with MOSAIC_SIM_MODE=replay (and the same CASSETTE_DIR) after banking a live run'}
    >
      Replay last run
      <HelpTip
        text={replayEnabled
          ? 'Process is in cassette Replay. This re-fetches advisories through banked Terra/Sol recordings — free, no OpenAI call. Different from “Refresh advice”, which re-polls the same path without claiming banked-cassette semantics.'
          : 'Free cassette replay is process-level only. Bank a live run with MOSAIC_SIM_MODE=live (record), then restart with MOSAIC_SIM_MODE=replay and the same MOSAIC_CASSETTE_DIR. This button does not hot-swap mode mid-process.'}
        label="About Replay last run"
      />
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
