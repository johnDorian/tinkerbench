<script>
  import { onMount } from 'svelte'
  import { api, ago } from './api.js'
  import Modal from './Modal.svelte'

  /** models: library (to show what is already imported); onimport(project) -> Model | undefined */
  let { models = [], onclose, onimport } = $props()

  let projects = $state(null)
  let loadErr = $state('')
  let busy = $state('')

  onMount(async () => {
    try {
      projects = await api.openscadProjects()
    } catch (e) {
      loadErr = e.message
    }
  })

  async function imp(name) {
    busy = name
    try {
      await onimport(name)
    } finally {
      busy = ''
    }
  }
</script>

<Modal title="From OpenSCAD" {onclose}>
  <p class="muted small">Imports the last rendered STL of an OpenSCAD workbench project into the model library.</p>
  {#if loadErr}
    <p class="danger-text">{loadErr}</p>
  {:else if projects === null}
    <p class="muted">Loading projects…</p>
  {:else if !projects.length}
    <p class="muted">No OpenSCAD projects.</p>
  {:else}
    <ul class="list">
      {#each projects as p (p.name)}
        {@const imported = models.some((m) => m.openscad_project === p.name)}
        <li>
          <div class="info">
            <span class="name">{p.name}</span>
            {#if p.description}<span class="muted small">{p.description}</span>{/if}
            <span class="muted small">{p.rendered ? 'rendered' : 'not rendered yet'}{p.updated ? ` · ${ago(p.updated)}` : ''}</span>
          </div>
          <button class:primary={!imported} disabled={!p.rendered || busy !== ''} onclick={() => imp(p.name)}>
            {busy === p.name ? 'Importing…' : imported ? 'Re-import' : 'Import'}
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</Modal>

<style>
  .list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 8px; }
  li { display: flex; justify-content: space-between; align-items: center; gap: 10px; border-bottom: 1px solid var(--line); padding-bottom: 8px; }
  .info { display: flex; flex-direction: column; min-width: 0; }
  .name { font-weight: 600; }
  p { margin: 0 0 10px; }
</style>
