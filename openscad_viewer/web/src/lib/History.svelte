<script>
  // Saved versions of the project, newest first. Clicking one previews it in the code pane.
  import { ago } from './api.js'

  /** versions: [{n, file, time, source, note, deleted}]; active: previewed version number */
  let { versions = [], active = null, onpick } = $props()

  const newestFirst = $derived([...versions].reverse())
</script>

<ol class="history">
  {#each newestFirst as v (v.n)}
    <li>
      <button class:active={v.n === active} disabled={v.deleted} onclick={() => onpick(v)}>
        <span class="n">#{v.n}</span>
        <span class="file">{v.deleted ? `deleted ${v.file}` : v.file}</span>
        <span class="by">{v.source || '?'}</span>
        <span class="time">{ago(v.time)}</span>
        {#if v.note}<span class="note">{v.note}</span>{/if}
      </button>
    </li>
  {:else}
    <li class="empty">No versions yet</li>
  {/each}
</ol>

<style>
  .history { list-style: none; margin: 0; padding: 4px; overflow: auto; height: 100%; box-sizing: border-box; }
  button {
    width: 100%; text-align: left; background: none; border: 0; border-radius: 6px; padding: 6px 8px;
    display: grid; grid-template-columns: auto 1fr auto; gap: 2px 8px; color: var(--text); cursor: pointer; font-size: 13px;
  }
  button:hover:not(:disabled) { background: var(--bg); }
  button.active { background: var(--accent-soft); }
  button:disabled { cursor: default; opacity: 0.6; }
  .n { color: var(--faint); font: 12px var(--mono); }
  .file { font: 12px var(--mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .time { color: var(--muted); font-size: 12px; grid-row: 1; grid-column: 3; }
  .by { grid-column: 2; color: var(--muted); font-size: 12px; }
  .note { grid-column: 1 / -1; color: var(--muted); font-size: 12px; }
  .empty { color: var(--muted); padding: 8px; font-size: 13px; }
</style>
