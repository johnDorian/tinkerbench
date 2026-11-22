<script>
  // Header chip: live printer state from the SSE printer events, with pause/resume/cancel and the webcam.
  let { printer = null, webcamUrl = '', printerEnabled = false, onaction } = $props()

  let showCam = $state(false)
  let camError = $state(false)

  const state = $derived(
    !printerEnabled ? 'disabled' : !printer ? 'unknown' : !printer.online ? 'offline' : printer.state ?? 'unknown',
  )
  const label = $derived(
    { disabled: 'Printing off', unknown: 'Printer…', offline: 'Printer offline', standby: 'Idle', printing: 'Printing', paused: 'Paused', complete: 'Complete', cancelled: 'Cancelled', error: 'Error' }[state] ?? state,
  )
  const tone = $derived(
    state === 'printing' ? 'run' : state === 'paused' ? 'warn' : state === 'error' ? 'bad' : state === 'standby' || state === 'complete' ? 'ok' : 'off',
  )
  const busy = $derived(state === 'printing' || state === 'paused')
  const pct = $derived(printer?.progress != null ? Math.round(printer.progress * 100) : null)
  const temps = (t) => (t ? `${Math.round(t.temp)}/${Math.round(t.target)}°` : '')

  function cancel() {
    if (confirm('Cancel the print? The printer stops; the job cannot be resumed.')) onaction('cancel')
  }
</script>

<div class="chip {tone}" title={printer?.message || ''}>
  <span class="dot"></span>
  <b>{label}</b>
  {#if pct !== null && (busy || state === 'complete')}<span>{pct}%</span>{/if}
  {#if printer?.filename && busy}<span class="file" title={printer.filename}>{printer.filename}</span>{/if}
  {#if printer?.extruder && printer.online}
    <span class="temps">E {temps(printer.extruder)} · B {temps(printer.bed)}</span>
  {/if}
  {#if state === 'printing'}<button onclick={() => onaction('pause')}>Pause</button>{/if}
  {#if state === 'paused'}<button onclick={() => onaction('resume')}>Resume</button>{/if}
  {#if busy}<button class="danger" onclick={cancel}>Cancel</button>{/if}
  {#if webcamUrl}<button onclick={() => (showCam = !showCam)}>{showCam ? 'Hide camera' : 'Camera'}</button>{/if}
</div>

{#if showCam && webcamUrl}
  <div class="cam panel">
    {#if camError}
      <p class="muted">Camera unavailable.</p>
    {:else}
      <img src={webcamUrl} alt="Printer camera" onerror={() => (camError = true)} />
    {/if}
  </div>
{/if}

<style>
  .chip {
    display: flex; flex-wrap: wrap; gap: 6px; align-items: center; font-size: 13px; min-width: 0;
    border: 1px solid var(--line); border-radius: 999px; padding: 3px 6px 3px 10px; background: var(--card);
  }
  .dot { width: 9px; height: 9px; border-radius: 50%; background: var(--faint); }
  .chip.run .dot { background: var(--accent); }
  .chip.warn .dot { background: var(--warn); }
  .chip.ok .dot { background: var(--ok); }
  .chip.bad .dot, .chip.bad b { color: var(--err); }
  .chip.bad .dot { background: var(--err); }
  .file { color: var(--muted); max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .temps { color: var(--muted); font-size: 12px; white-space: nowrap; }
  .chip button { font-size: 12px; padding: 2px 8px; }
  .cam { position: absolute; right: 16px; top: 52px; width: min(420px, calc(100% - 32px)); z-index: 20; }
  .cam img { display: block; width: 100%; }
  .cam p { margin: 10px; }
</style>
