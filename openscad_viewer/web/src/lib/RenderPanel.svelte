<script>
  // Last render: size/volume, bed fit, errors, warnings, echo() output, params and the raw log.
  import { ago } from './api.js'

  /** result: render.Result; params: stored {name: expr}; rendering: bool; bed: [x, y] */
  let { result = null, params = {}, rendering = false, bed = [220, 220], onrender } = $props()

  let showLog = $state(false)
  let editingParams = $state(false)
  let paramText = $state('')

  const s = $derived(result?.stats)
  const fmt = (n) => (Math.round(n * 10) / 10).toLocaleString()
  const offBed = $derived(s && (s.size[0] > bed[0] || s.size[1] > bed[1]))
  const belowBed = $derived(s && s.min[2] < -0.001)
  const floating = $derived(s && s.min[2] > 0.001)

  function editParams() {
    paramText = Object.entries(params ?? {}).map(([k, v]) => `${k} = ${v}`).join('\n')
    editingParams = true
  }

  function applyParams() {
    const out = {}
    for (const line of paramText.split('\n')) {
      const m = line.match(/^\s*([A-Za-z_$][A-Za-z0-9_]*)\s*=\s*(.+?);?\s*$/)
      if (m) out[m[1]] = m[2]
    }
    editingParams = false
    onrender(out)
  }
</script>

<div class="panel">
  <div class="row">
    {#if rendering}
      <span class="pill busy">Rendering…</span>
    {:else if result}
      <span class="pill" class:ok={result.ok} class:bad={!result.ok}>{result.ok ? 'Rendered' : 'Render failed'}</span>
      <span class="muted">{(result.duration_ms / 1000).toFixed(1)} s · {ago(result.started)}</span>
    {:else}
      <span class="muted">Not rendered yet</span>
    {/if}
    <span class="spacer"></span>
    <button onclick={editParams} disabled={rendering}>Params{Object.keys(params ?? {}).length ? ` (${Object.keys(params).length})` : ''}</button>
    <button class="primary" onclick={() => onrender(null)} disabled={rendering}>Render</button>
  </div>

  {#if s}
    <div class="stats">
      <span><b>{fmt(s.size[0])} × {fmt(s.size[1])} × {fmt(s.size[2])}</b> mm</span>
      <span>{fmt(s.volume_mm3 / 1000)} cm³</span>
      <span>{s.triangles.toLocaleString()} triangles</span>
      {#if result && !result.ok}<span class="warn">showing the last successful render</span>{/if}
      {#if offBed}<span class="warn">larger than the {bed[0]}×{bed[1]} mm bed</span>{/if}
      {#if belowBed}<span class="warn">goes below the bed (min z {fmt(s.min[2])})</span>{/if}
      {#if floating}<span class="warn">floats {fmt(s.min[2])} mm above the bed</span>{/if}
    </div>
  {/if}

  {#if editingParams}
    <div class="params">
      <textarea bind:value={paramText} rows="4" spellcheck="false" placeholder="width = 40&#10;label = &quot;A&quot;"></textarea>
      <div class="row">
        <span class="muted">One <code>name = OpenSCAD value</code> per line; overrides top-level variables. Empty clears.</span>
        <span class="spacer"></span>
        <button onclick={() => (editingParams = false)}>Cancel</button>
        <button class="primary" onclick={applyParams}>Render with params</button>
      </div>
    </div>
  {:else if Object.keys(params ?? {}).length}
    <div class="msgs muted">Params: {Object.entries(params).map(([k, v]) => `${k}=${v}`).join(', ')}</div>
  {/if}

  {#if result}
    <div class="msgs">
      {#each result.errors ?? [] as m}<div class="err">{m}</div>{/each}
      {#each result.warnings ?? [] as m}<div class="warn">{m}</div>{/each}
      {#each result.echo ?? [] as m}<div class="muted">{m}</div>{/each}
    </div>
    {#if result.log}
      <button class="link" onclick={() => (showLog = !showLog)}>{showLog ? 'Hide' : 'Show'} full log</button>
      {#if showLog}<pre class="log">{result.log}</pre>{/if}
    {/if}
  {/if}
</div>

<style>
  .panel { padding: 8px 12px; display: flex; flex-direction: column; gap: 6px; font-size: 13px; overflow: auto; max-height: 100%; }
  .row { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
  .spacer { flex: 1; }
  .muted { color: var(--muted); }
  .pill { padding: 2px 8px; border-radius: 999px; font-weight: 600; font-size: 12px; background: var(--accent-soft); }
  .pill.ok { background: var(--ok-soft); color: var(--ok); }
  .pill.bad { background: var(--err-soft); color: var(--err); }
  .pill.busy { animation: pulse 1s ease-in-out infinite alternate; }
  @keyframes pulse { to { opacity: 0.5; } }
  .stats { display: flex; gap: 14px; flex-wrap: wrap; }
  .msgs { font: 12px/1.5 var(--mono); display: flex; flex-direction: column; gap: 2px; }
  .err { color: var(--err); }
  .warn { color: var(--warn); }
  .params textarea {
    width: 100%; box-sizing: border-box; font: 12px var(--mono); background: var(--card); color: var(--text);
    border: 1px solid var(--line); border-radius: 6px; padding: 6px;
  }
  .link { background: none; border: 0; color: var(--accent); padding: 0; cursor: pointer; align-self: flex-start; font-size: 12px; }
  .log {
    font: 11px/1.4 var(--mono); background: var(--bg); border: 1px solid var(--line); border-radius: 6px;
    padding: 8px; margin: 0; white-space: pre-wrap; max-height: 240px; overflow: auto;
  }
</style>
