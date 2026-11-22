<script>
  import { ago } from './api.js'

  let { plates = [], current = '', onopen, onnew } = $props()
</script>

<section class="block">
  <div class="head">
    <h2 class="section-title">Plates</h2>
    <button onclick={onnew}>+ New</button>
  </div>
  <ul class="list">
    {#each plates as p (p.id)}
      <li>
        <button class="item" class:active={p.id === current} onclick={() => onopen(p.id)}>
          <span class="name">{p.name}</span>
          <span class="meta">{p.objects?.length ?? 0} object{p.objects?.length === 1 ? '' : 's'}{p.updated ? ` · ${ago(p.updated)}` : ''}</span>
        </button>
      </li>
    {:else}
      <li class="muted nothing">No plates yet.</li>
    {/each}
  </ul>
</section>

<style>
  .block { padding: 10px; border-bottom: 1px solid var(--line); }
  .head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 6px; }
  .list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
  .item {
    width: 100%; text-align: left; display: flex; flex-direction: column; gap: 1px; border: 1px solid transparent;
    background: none; padding: 5px 8px;
  }
  .item:hover { background: var(--accent-soft); }
  .item.active { border-color: var(--accent); background: var(--accent-soft); }
  .name { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .meta { font-size: 12px; color: var(--muted); }
  .nothing { font-size: 13px; padding: 4px 8px; }
</style>
