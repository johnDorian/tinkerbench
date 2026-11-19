<script>
  import { api, ago } from './api.js'
  import { renderMarkdown } from './markdown.js'

  // name: project; view: the view currently shown on the canvas (default for the table);
  // refresh: changes whenever the project does, so the page reloads.
  let { name, view = 'breadboard', refresh = 0 } = $props()
  const VIEWS = ['breadboard', 'schematic', 'pcb']

  let tableView = $state(view)
  let w = $state(null)
  let error = $state('')
  let draft = $state(null) // edited notes, null = unchanged
  let saving = $state(false)
  let copied = $state(false)
  let showPins = $state(false)

  $effect(() => { name; tableView = view })
  $effect(() => { name; draft = null })

  async function load() {
    try { error = ''; w = await api.wiring(name, tableView) } catch (e) { error = e.message }
  }
  $effect(() => { name; tableView; refresh; if (name) load() })

  async function save() {
    saving = true
    try { error = ''; w = await api.saveNotes(name, draft, tableView); draft = null } catch (e) { error = e.message }
    saving = false
  }
  async function copy() {
    try {
      await navigator.clipboard.writeText(await api.wiringMarkdown(name, tableView))
      copied = true
      setTimeout(() => (copied = false), 1500)
    } catch (e) { error = e.message }
  }
  const notes = $derived(draft ?? w?.notes ?? '')
  const warns = $derived((w?.warnings ?? []).filter((x) => x.level !== 'info'))
  const infos = $derived((w?.warnings ?? []).filter((x) => x.level === 'info'))
</script>

{#if error}<div class="msg bad">{error}</div>{/if}
{#if w}
  <div class="bar">
    <button onclick={copy}>{copied ? 'Copied' : 'Copy as Markdown'}</button>
    <a class="button" href={api.wiringMarkdownUrl(name, tableView, true)}>Download wiring.md</a>
  </div>

  {#if w.stale}
    <div class="msg warn">Notes were written at sketch v{w.notes_version}, the sketch is now v{w.sketch_version}: check them.</div>
  {/if}
  {#each warns as x}
    <div class="msg {x.level === 'error' ? 'bad' : 'warn'}">{x.level === 'error' ? 'Error: ' : ''}{x.text}</div>
  {/each}

  <section>
    <div class="row"><h4>Notes</h4>
      {#if draft === null}<button onclick={() => (draft = w.notes)}>{w.notes ? 'Edit' : 'Write notes'}</button>{/if}
    </div>
    {#if draft !== null}
      <textarea spellcheck="false" placeholder={'## Purpose\n\n## Power\n\n## Build steps\n\n## Check before powering up\n\n## Firmware\n\n## Not done / open questions'} value={draft} oninput={(e) => (draft = e.target.value)}></textarea>
      <div class="row">
        <span><button class="primary" disabled={saving || draft === w.notes} onclick={save}>Save</button>
          <button disabled={saving} onclick={() => (draft = null)}>Revert</button></span>
        <span class="faint">Markdown · saved as notes.md, versioned</span>
      </div>
    {:else if notes.trim()}
      <div class="md">{@html renderMarkdown(notes)}</div>
    {:else}
      <p class="faint">No notes yet. Purpose, power, build steps and what to check on the real parts go here; an agent can write them with set_notes.</p>
    {/if}
  </section>

  <section>
    <div class="row"><h4>Connections</h4>
      <span class="tabs">{#each VIEWS as v}<button class:on={tableView === v} onclick={() => (tableView = v)}>{v}</button>{/each}</span>
    </div>
    {#if w.connections.length}
      {@const colour = w.connections.some((c) => c.color)}
      <table>
        <thead><tr><th>Net</th><th>From</th><th>To</th>{#if colour}<th>Wire</th>{/if}</tr></thead>
        <tbody>
          {#each w.connections as c}
            <tr><td>{c.net}</td><td>{c.from}</td><td>{c.to}</td>{#if colour}<td>{c.color}</td>{/if}</tr>
          {/each}
        </tbody>
      </table>
    {:else}<p class="faint">Nothing is connected in the {tableView} view.</p>{/if}
    {#if w.layout?.wires}
      <p class="faint">{w.layout.wires} wires · {Math.round(w.layout.wire_length_mm)} mm of wire · {w.layout.crossings} crossings</p>
    {/if}
  </section>

  {#if w.pin_usage.length}
    <section>
      <div class="row"><h4>Pins in use</h4><button onclick={() => (showPins = !showPins)}>{showPins ? 'Hide' : 'Show'}</button></div>
      {#if showPins}
        {#each w.pin_usage as p}
          <strong>{p.part}</strong>
          <ul>{#each p.pins as l}<li>{l.pin}, net {l.net} → {l.goes_to.join(', ')}</li>{/each}</ul>
        {/each}
      {/if}
    </section>
  {/if}

  <section>
    <h4>Parts</h4>
    {#if w.parts.length}
      <table>
        <thead><tr><th>Qty</th><th>Part</th><th>Value</th><th>Titles</th></tr></thead>
        <tbody>
          {#each w.parts as p}
            <tr><td>{p.count}</td><td>{p.name}</td><td>{[p.properties, p.package].filter(Boolean).join(', ')}</td><td>{p.titles.join(', ')}</td></tr>
          {/each}
        </tbody>
      </table>
    {:else}<p class="faint">The sketch has no parts.</p>{/if}
  </section>

  {#if infos.length}
    <section>
      <h4>Also noticed</h4>
      <ul>{#each infos as x}<li class="faint">{x.text}</li>{/each}</ul>
    </section>
  {/if}
{:else if !error}
  <p class="faint">Loading…</p>
{/if}

<style>
  .bar, .row { display: flex; gap: 8px; align-items: center; justify-content: space-between; flex-wrap: wrap; }
  .bar { justify-content: flex-start; }
  .tabs { display: flex; gap: 4px; }
  .msg { padding: 6px 10px; border-radius: 6px; font-size: 13px; }
  .msg.bad { background: var(--err-soft); color: var(--err); }
  .msg.warn { background: var(--warn-soft); color: var(--warn); }
  section { display: flex; flex-direction: column; gap: 6px; }
  h4 { margin: 8px 0 2px; }
  .faint { color: var(--faint); font-size: 12px; margin: 0; }
  table, .md :global(table) { font-size: 13px; border-collapse: collapse; width: 100%; }
  th { text-align: left; color: var(--faint); font-weight: 600; }
  td, th, .md :global(td), .md :global(th) { padding: 3px 6px 3px 0; border-bottom: 1px solid var(--line); vertical-align: top; }
  textarea { min-height: 360px; width: 100%; box-sizing: border-box; font: 12px/1.4 var(--mono); resize: vertical; }
  ul { margin: 2px 0; padding-left: 18px; font-size: 13px; }
  .md { font-size: 14px; line-height: 1.45; }
  .md :global(h2), .md :global(h3), .md :global(h4) { margin: 10px 0 2px; font-size: 14px; }
  .md :global(p) { margin: 4px 0; }
  .md :global(ul), .md :global(ol) { margin: 2px 0 6px; padding-left: 20px; }
  .md :global(code) { font: 12px var(--mono); background: var(--line); padding: 0 3px; border-radius: 3px; }
  .md :global(pre) { background: var(--line); padding: 6px; border-radius: 4px; }
  .md :global(blockquote) { margin: 4px 0; padding: 2px 8px; border-left: 3px solid var(--line); }
</style>
