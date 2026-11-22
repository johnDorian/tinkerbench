<script>
  import { dims } from './api.js'

  let { models = [], canAdd = false, onadd, ondelete, onupload, onimport, onreimport } = $props()
  let over = $state(false)
  let input

  function drop(e) {
    e.preventDefault()
    over = false
    const files = [...(e.dataTransfer?.files ?? [])]
    if (files.length) onupload(files)
  }
  function pick(e) {
    const files = [...e.currentTarget.files]
    e.currentTarget.value = ''
    if (files.length) onupload(files)
  }
</script>

<section class="block">
  <div class="head">
    <h2 class="section-title">Models</h2>
    <button onclick={onimport}>From OpenSCAD</button>
  </div>

  <div
    class="drop"
    class:over
    ondragover={(e) => { e.preventDefault(); over = true }}
    ondragleave={() => (over = false)}
    ondrop={drop}
    role="region"
    aria-label="Drop STL files here"
  >
    <span class="muted">Drop .stl files here or</span>
    <button onclick={() => input.click()}>Choose file</button>
    <input bind:this={input} type="file" accept=".stl,model/stl" multiple hidden onchange={pick} />
  </div>

  <ul class="list">
    {#each models as m (m.id)}
      <li class="model">
        <div class="top">
          <span class="name" title={m.name}>{m.name}</span>
          <span class="tag">{m.source === 'openscad' ? 'OpenSCAD' : 'upload'}</span>
        </div>
        <div class="meta">{dims(m.size)}{m.triangles ? ` · ${m.triangles.toLocaleString()} tris` : ''}</div>
        <div class="actions">
          <button class="primary" disabled={!canAdd} onclick={() => onadd(m.id)} title={canAdd ? 'Add to the open plate' : 'Open a plate first'}>Add</button>
          {#if m.openscad_project}
            <button onclick={() => onreimport(m)} title="Re-import the last render from OpenSCAD">Re-import</button>
          {/if}
          <button class="danger" onclick={() => ondelete(m)}>Delete</button>
        </div>
      </li>
    {:else}
      <li class="muted nothing">No models yet.</li>
    {/each}
  </ul>
</section>

<style>
  .block { padding: 10px; min-height: 0; }
  .head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 6px; gap: 6px; }
  .drop {
    display: flex; flex-wrap: wrap; gap: 6px; align-items: center; justify-content: center; font-size: 13px;
    border: 1px dashed var(--line); border-radius: 8px; padding: 8px; margin-bottom: 8px;
  }
  .drop.over { border-color: var(--accent); background: var(--accent-soft); }
  .list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 6px; }
  .model { border: 1px solid var(--line); border-radius: 8px; padding: 6px 8px; }
  .top { display: flex; justify-content: space-between; gap: 6px; align-items: baseline; }
  .name { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
  .tag { font-size: 11px; color: var(--muted); border: 1px solid var(--line); border-radius: 999px; padding: 0 6px; white-space: nowrap; }
  .meta { font-size: 12px; color: var(--muted); margin: 2px 0 6px; }
  .actions { display: flex; flex-wrap: wrap; gap: 4px; }
  .actions button { font-size: 12px; padding: 2px 8px; }
  .nothing { font-size: 13px; }
</style>
