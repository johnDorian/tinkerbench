<script>
  // Slice button, live status and log, the result, downloads and the Print button with its confirm dialog.
  import { api, hm } from './api.js'
  import Modal from './Modal.svelte'

  /** plate: open Plate; slice: SliceResult or null; printer: Printer or null; onslice(), onprint() -> Promise */
  let { plate, slice = null, printer = null, printerEnabled = false, onslice, onprint } = $props()

  let confirming = $state(false)
  let sending = $state(false)
  let logEl = $state()

  const status = $derived(slice?.status ?? 'idle')
  const running = $derived(status === 'queued' || status === 'slicing')
  const done = $derived(status === 'done')
  const printerBusy = $derived(!!printer && (printer.state === 'printing' || printer.state === 'paused'))

  // Why Print is disabled; empty means it can be pressed.
  const reasons = $derived.by(() => {
    const r = []
    if (!printerEnabled) r.push('printing is off on this server')
    else if (!printer?.online) r.push('printer offline')
    else if (printerBusy) r.push(`printer is ${printer.state}`)
    if (!done) r.push('slice the plate first')
    else if (slice.stale) r.push('the plate changed since the last slice')
    return r
  })

  const hasResult = $derived(!!slice && (done || !!slice.gcode_name))

  $effect(() => {
    slice?.log // follow the log while it grows
    if (logEl) logEl.scrollTop = logEl.scrollHeight
  })

  async function sendPrint() {
    sending = true
    try {
      await onprint()
    } finally {
      sending = false
      confirming = false
    }
  }
</script>

<section class="block">
  <div class="row">
    <button class="primary" disabled={running || !plate.objects.length} onclick={onslice}>
      {running ? 'Slicing…' : status === 'idle' ? 'Slice' : 'Slice again'}
    </button>
    <span class="pill {status}">{status}</span>
    {#if slice?.stale}<span class="pill stale" title="The plate changed after this slice. Slice again before printing.">stale</span>{/if}
    {#if slice?.started && running}<span class="muted small">started {new Date(slice.started).toLocaleTimeString()}</span>{/if}
  </div>

  {#if slice?.error}
    <div class="err">{slice.error}</div>
  {/if}

  {#if slice?.log}
    <pre class="log" bind:this={logEl}>{slice.log}</pre>
  {/if}

  {#if slice?.warnings?.length}
    <ul class="warnings">
      {#each slice.warnings as w}<li>{w}</li>{/each}
    </ul>
  {/if}

  {#if hasResult && slice.estimated_time_s !== undefined}
    <dl class="result">
      <dt>Time</dt><dd>{hm(slice.estimated_time_s)}</dd>
      <dt>Filament</dt><dd>{slice.filament_g != null ? `${Number(slice.filament_g).toFixed(1)} g` : '–'}{slice.filament_mm ? ` · ${Math.round(slice.filament_mm / 1000)} m` : ''}</dd>
      <dt>Layers</dt><dd>{slice.layers ?? '–'}</dd>
      <dt>Height</dt><dd>{slice.max_z != null ? `${Number(slice.max_z).toFixed(1)} mm` : '–'}</dd>
    </dl>
  {/if}

  {#if hasResult}
    <div class="downloads">
      <a class="button" href={api.gcodeUrl(plate.id, true)} download>G-code</a>
      <a class="button" href={api.threemfUrl(plate.id, true)} download>3MF</a>
      {#if slice.gcode_name}<span class="muted small file" title={slice.gcode_name}>{slice.gcode_name}</span>{/if}
    </div>
  {/if}

  <div class="print">
    <button class="primary big" disabled={reasons.length > 0} onclick={() => (confirming = true)}>Print</button>
    {#if reasons.length}<span class="muted small">{reasons.join(' · ')}</span>{/if}
  </div>
</section>

{#if confirming}
  <Modal title="Print “{plate.name}”?" onclose={() => (confirming = false)}>
    <dl class="confirm">
      <dt>File</dt><dd>{slice?.gcode_name ?? '–'}</dd>
      <dt>Filament</dt><dd>{slice?.filament_g != null ? `${Number(slice.filament_g).toFixed(1)} g` : '–'}</dd>
      <dt>Time</dt><dd>{hm(slice?.estimated_time_s)}</dd>
      <dt>Printer</dt><dd>{printer?.state ?? 'unknown'}{printer?.online === false ? ' (offline)' : ''}</dd>
    </dl>
    <p class="muted small">The G-code is uploaded to the Kobra 3 and starts straight away. Auto bed levelling runs first if it is set on the printer.</p>
    <div class="actions">
      <button onclick={() => (confirming = false)}>Cancel</button>
      <button class="primary" disabled={sending} onclick={sendPrint}>{sending ? 'Sending…' : 'Start print'}</button>
    </div>
  </Modal>
{/if}

<style>
  .block { padding: 10px 12px; border-bottom: 1px solid var(--line); display: flex; flex-direction: column; gap: 8px; min-height: 0; }
  .row { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  .pill { font-size: 12px; padding: 1px 8px; border-radius: 999px; border: 1px solid var(--line); color: var(--muted); }
  .pill.done { color: var(--ok); border-color: var(--ok); }
  .pill.error { color: var(--err); border-color: var(--err); }
  .pill.queued, .pill.slicing { color: var(--warn); border-color: var(--warn); }
  .pill.stale { color: var(--warn); border-color: var(--warn); background: var(--warn-soft); }
  .err { color: var(--err); background: var(--err-soft); border-radius: 6px; padding: 6px 8px; font-size: 13px; white-space: pre-wrap; }
  .log {
    margin: 0; max-height: 150px; overflow: auto; font: 11px/1.4 var(--mono); background: var(--bg);
    border: 1px solid var(--line); border-radius: 6px; padding: 6px 8px; white-space: pre-wrap; word-break: break-word;
  }
  .warnings { margin: 0; padding-left: 18px; color: var(--warn); font-size: 13px; }
  .result { display: grid; grid-template-columns: auto 1fr; gap: 2px 10px; margin: 0; font-size: 13px; }
  .result dt, .confirm dt { color: var(--muted); }
  .result dd, .confirm dd { margin: 0; }
  .downloads { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }
  .file { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 100%; }
  .print { display: flex; flex-direction: column; gap: 4px; align-items: flex-start; }
  .big { font-size: 15px; padding: 7px 18px; }
  .confirm { display: grid; grid-template-columns: auto 1fr; gap: 4px 12px; margin: 0 0 10px; }
  .actions { display: flex; justify-content: flex-end; gap: 8px; }
</style>
