<script>
  import { onMount } from 'svelte'
  import { api, who, ago } from './lib/api.js'
  import Canvas from './lib/Canvas.svelte'
  import Wiring from './lib/Wiring.svelte'

  const VIEWS = ['breadboard', 'schematic', 'pcb']
  let config = $state({ fritzing_version: '', mcp_url: '', parts: 0 })
  let projects = $state([])
  let current = $state(decodeURIComponent(location.hash.slice(2)) || '')
  let project = $state(null)
  let view = $state('breadboard')
  let side = $state('parts')
  let lastCanvas = $state('breadboard') // the drawing view last shown, default for the wiring table
  let opened = '' // project the side tab was last chosen for
  let rendering = $state(false)
  let stamp = $state(0)
  let error = $state('')
  let connected = $state(false)
  let activity = $state([])
  let draft = $state(null) // edited sketch.fz text, null = unchanged
  let versions = $state([])
  let query = $state('')
  let hits = $state([])
  let searching = $state(false)
  let detail = $state(null)
  let mineOnly = $state(false)
  let importUrl = $state('')
  let importing = $state(false)
  let imported = $state(null) // result of the last import

  const guard = async (fn) => { try { error = ''; return await fn() } catch (e) { error = e.message } }
  const sketchFile = $derived(project?.content?.find((f) => f.name === 'sketch.fz')?.content ?? '')
  const lr = $derived(project?.last_render)
  const hasView = $derived(lr?.views?.includes(view))
  $effect(() => { if (view !== 'wiring') lastCanvas = view })

  async function loadProjects() {
    projects = (await guard(api.projects)) ?? projects
    if (!current && projects.length) select(projects[0].name)
    // a refresh keeps the project in the URL hash but nothing has loaded it yet
    else if (current && !project) loadProject()
  }
  async function loadProject() {
    if (!current) { project = null; return }
    const p = await guard(() => api.project(current))
    if (p) {
      project = p
      // coming back to a project that has notes lands on its wiring page
      if (opened !== current) { opened = current; if (p.has_notes) view = 'wiring' }
    }
  }
  function select(name) { current = name; location.hash = '/' + encodeURIComponent(name); draft = null; versions = []; loadProject() }

  async function create() {
    const name = prompt('Project name (lowercase letters, digits, - and _)')
    if (!name) return
    const info = await guard(() => api.create(name, ''))
    if (info) { await loadProjects(); select(info.name) }
  }
  async function remove() {
    if (!confirm(`Delete project ${current}? (moved to the trash folder)`)) return
    await guard(() => api.remove(current))
    current = ''; project = null; await loadProjects()
  }
  async function save() {
    const r = await guard(() => api.saveFile(current, 'sketch.fz', draft))
    if (r) draft = null
  }
  async function render() { rendering = true; await guard(() => api.render(current)); rendering = false }

  async function search() {
    searching = true
    hits = (await guard(() => api.search(query, mineOnly ? 'user' : ''))) ?? []
    searching = false
  }
  async function doImport(fn) {
    importing = true
    imported = null
    const r = await guard(fn)
    importing = false
    if (!r) return
    imported = r
    importUrl = ''
    query = r.module_id
    mineOnly = true
    await search()
    detail = await guard(() => api.part(r.module_id))
  }
  async function importFromUrl() {
    const url = importUrl.trim()
    if (!url) return
    const again = async (call) => { try { error = ''; return await call(false) } catch (e) { if (/overwrite/.test(e.message) && confirm(e.message + '\n\nReplace it?')) { return await call(true) } throw e } }
    await doImport(() => again((o) => api.importUrl(url, o)))
  }
  async function importFromFile(e) {
    const file = e.target.files?.[0]
    e.target.value = ''
    if (!file) return
    const again = async (call) => { try { error = ''; return await call(false) } catch (err) { if (/overwrite/.test(err.message) && confirm(err.message + '\n\nReplace it?')) { return await call(true) } throw err } }
    await doImport(() => again((o) => api.importFile(file, o)))
  }
  async function deleteImported(d) {
    if (!confirm(`Delete the imported part ${d.title}?`)) return
    try {
      await api.deleteUserPart(d.module_id, false)
    } catch (e) {
      if (/force/.test(e.message) && confirm(e.message + '\n\nDelete it anyway? Those sketches will fail to render.')) await guard(() => api.deleteUserPart(d.module_id, true))
      else { error = e.message; return }
    }
    detail = null
    await search()
  }
  async function show(h) { detail = await guard(() => api.part(h.module_id)) }
  async function add(h) { await guard(() => api.addPart(current, h.module_id)) }
  async function removePart(t) { if (confirm(`Remove ${t} and its wires?`)) await guard(() => api.removePart(current, t)) }
  async function loadHistory() { versions = (await guard(() => api.history(current))) ?? [] }
  async function restore(n) { if (confirm(`Restore version ${n} as a new version?`)) await guard(() => api.restore(current, n)) }

  $effect(() => { if (side === 'history' && current) loadHistory() })

  onMount(() => {
    api.config().then((c) => (config = c)).catch(() => {})
    loadProjects()
    const es = new EventSource('/api/events')
    es.onopen = () => (connected = true)
    es.onerror = () => (connected = false)
    es.onmessage = (m) => {
      const e = JSON.parse(m.data)
      if (e.kind === 'project_created' || e.kind === 'project_deleted') loadProjects()
      if (e.project !== current) return
      activity = [e, ...activity].slice(0, 30)
      if (e.kind === 'render_started') rendering = true
      if (e.kind === 'render_done') { rendering = false; stamp++ }
      if (e.kind !== 'render_started') loadProject()
      if (e.kind === 'file_changed' && side === 'history') loadHistory()
    }
    return () => es.close()
  })
</script>

<div class="app">
  <header>
    <strong>Fritzing</strong>
    <select value={current} onchange={(e) => select(e.target.value)}>
      {#each projects as p}<option value={p.name}>{p.name}</option>{/each}
    </select>
    <button onclick={create}>New</button>
    {#if current}
      <button onclick={render} disabled={rendering}>{rendering ? 'Rendering…' : 'Render'}</button>
      <a class="button" href={api.downloadUrl(current)}>Download .fzz</a>
      <button class="danger" onclick={remove}>Delete</button>
    {/if}
    <span class="grow"></span>
    {#if error}<span class="err">{error}</span>{/if}
    <span class="dot" class:on={connected} title={connected ? 'live' : 'disconnected'}></span>
    <span class="faint">Fritzing {config.fritzing_version} · {config.parts} parts · MCP {config.mcp_url}</span>
  </header>

  <main>
    <section class="left">
      <nav>
        {#each [...VIEWS, 'wiring'] as v}<button class:on={view === v} onclick={() => (view = v)}>{v}</button>{/each}
      </nav>
      {#if lr && !lr.ok}
        <div class="msg bad">{#each lr.errors as m}<div>{m}</div>{/each}{#if lr.log}<pre>{lr.log}</pre>{/if}</div>
      {/if}
      {#if lr?.warnings?.length}<div class="msg warn">{#each lr.warnings as m}<div>{m}</div>{/each}</div>{/if}
      {#if view === 'wiring'}
        <div class="wiring">{#if current}<Wiring name={current} view={lastCanvas} refresh={project?.version ?? 0} />{:else}<div class="empty">Create or pick a project.</div>{/if}</div>
      {:else if project && hasView}
        <Canvas src={api.viewUrl(current, view, stamp)} reset={current + view} />
      {:else}
        <div class="empty">{project ? (rendering ? 'Rendering…' : `No ${view} render yet.`) : 'Create or pick a project.'}</div>
      {/if}
      {#if lr && view !== 'wiring'}<div class="faint">rendered {ago(lr.time)} in {lr.duration_s?.toFixed(1)}s · scroll to zoom, drag to pan, double-click to reset</div>{/if}
    </section>

    <aside>
      <nav>
        {#each ['parts', 'sketch', 'code', 'history', 'activity'] as t}<button class:on={side === t} onclick={() => (side = t)}>{t}</button>{/each}
      </nav>

      {#if side === 'parts'}
        <form onsubmit={(e) => { e.preventDefault(); search() }}>
          <input placeholder="search parts: led, arduino uno, 10k…" bind:value={query} />
          <button class="primary" disabled={searching}>Search</button>
        </form>
        <label class="faint"><input type="checkbox" bind:checked={mineOnly} onchange={search} /> imported parts only</label>
        <details>
          <summary class="faint">Import a part (.fzpz)</summary>
          <form onsubmit={(e) => { e.preventDefault(); importFromUrl() }}>
            <input placeholder="https://…/part.fzpz (GitHub links work)" bind:value={importUrl} />
            <button disabled={importing || !importUrl.trim()}>Import</button>
          </form>
          <input type="file" accept=".fzpz,.zip" disabled={importing} onchange={importFromFile} />
          {#if imported}
            <p>Imported <strong>{imported.title}</strong> ({imported.connectors} pins, {imported.views.join(', ')}){imported.replaced ? ', replaced the old one' : ''}.</p>
            {#each imported.warnings as w}<p class="faint">⚠ {w}</p>{/each}
          {/if}
        </details>
        <ul class="list">
          {#each hits as h}
            <li>
              <img src={api.partImage(h.module_id, 'icon')} alt="" loading="lazy" onerror={(e) => (e.target.style.visibility = 'hidden')} />
              <div class="grow">
                <button class="link" onclick={() => show(h)}>{h.title}</button>
                <div class="faint">{h.family}{#if h.properties?.package} · {h.properties.package}{/if}{#if h.source === 'user'} · imported{/if}</div>
              </div>
              <button disabled={!current} onclick={() => add(h)}>Add</button>
            </li>
          {/each}
        </ul>
        {#if detail}
          <div class="card">
            <div class="row"><strong>{detail.title}</strong>{#if detail.source === 'user'}<button onclick={() => deleteImported(detail)}>Delete</button>{/if}<button onclick={() => (detail = null)}>×</button></div>
            <div class="faint">{detail.module_id}</div>
            <p>{detail.description}</p>
            <div class="imgs">{#each detail.views as v}<img src={api.partImage(detail.module_id, v)} alt={v} title={v} />{/each}</div>
            <table><tbody>
              {#each detail.connectors as c}<tr><td><code>{c.id}</code></td><td>{c.name}</td><td class="faint">{c.description ?? ''}</td></tr>{/each}
            </tbody></table>
          </div>
        {/if}
      {:else if side === 'sketch'}
        {#if project?.sketch}
          <h4>Parts</h4>
          <ul class="list">
            {#each project.sketch.parts as p}
              <li><div class="grow"><strong>{p.title}</strong> <span class="faint">{p.module_id}</span>
                {#if p.properties}<div class="faint">{Object.entries(p.properties).map(([k, v]) => `${k}=${v}`).join(', ')}</div>{/if}</div>
                <button class="danger" onclick={() => removePart(p.title)}>×</button></li>
            {:else}<li class="faint">Empty sketch.</li>{/each}
          </ul>
          <h4>Nets</h4>
          <ul class="list">
            {#each project.sketch.nets as n}<li><span class="faint">{n.view}</span> <code>{n.pins.join(' – ')}</code></li>{:else}<li class="faint">No connections.</li>{/each}
          </ul>
        {:else if project?.sketch_error}<div class="msg bad">{project.sketch_error}</div>{/if}
      {:else if side === 'code'}
        {#if project}
          <textarea spellcheck="false" value={draft ?? sketchFile} oninput={(e) => (draft = e.target.value)}></textarea>
          <div class="row"><button class="primary" disabled={draft === null} onclick={save}>Save</button>
            <button disabled={draft === null} onclick={() => (draft = null)}>Revert</button>
            <span class="faint">sketch.fz · XML is validated before saving</span></div>
        {/if}
      {:else if side === 'history'}
        <ul class="list">
          {#each [...versions].reverse() as v}
            <li><div class="grow">#{v.n} <strong>{v.file}</strong> <span class="faint">{who(v.source)} · {ago(v.time)}{v.note ? ' · ' + v.note : ''}</span></div>
              {#if !v.deleted}<button onclick={() => restore(v.n)}>Restore</button>{/if}</li>
          {/each}
        </ul>
      {:else}
        <ul class="list">
          {#each activity as e}<li><span class="faint">{ago(e.time)}</span> {who(e.source)}: {e.kind.replace('_', ' ')} {e.file ?? ''}{e.ok === false ? ' (failed)' : ''}</li>{:else}<li class="faint">Nothing yet.</li>{/each}
        </ul>
      {/if}
    </aside>
  </main>
</div>

<style>
  .app { display: flex; flex-direction: column; height: 100vh; }
  header { display: flex; gap: 8px; align-items: center; padding: 8px 12px; border-bottom: 1px solid var(--line); background: var(--card); flex-wrap: wrap; }
  main { flex: 1; display: grid; grid-template-columns: 1fr 380px; gap: 12px; padding: 12px; min-height: 0; }
  @media (max-width: 900px) { main { grid-template-columns: 1fr; grid-auto-rows: minmax(300px, auto); overflow: auto; } }
  .left, aside { display: flex; flex-direction: column; gap: 8px; min-height: 0; }
  aside { overflow: auto; background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 8px; }
  nav { display: flex; gap: 4px; flex-wrap: wrap; }
  .grow { flex: 1; min-width: 0; }
  .faint { color: var(--faint); font-size: 12px; }
  .err { color: var(--err); }
  .dot { width: 9px; height: 9px; border-radius: 50%; background: var(--err); }
  .dot.on { background: var(--ok); }
  .wiring { flex: 1; overflow: auto; background: var(--card); border: 1px solid var(--line); border-radius: 8px; padding: 12px 16px; display: flex; flex-direction: column; gap: 10px; }
  .wiring > :global(*) { max-width: 960px; width: 100%; }
  .empty { flex: 1; display: grid; place-items: center; color: var(--faint); border: 1px dashed var(--line); border-radius: 8px; }
  .msg { padding: 6px 10px; border-radius: 6px; font-size: 13px; }
  .msg.bad { background: var(--err-soft); color: var(--err); }
  .msg.warn { background: var(--warn-soft); color: var(--warn); }
  pre { white-space: pre-wrap; font: 11px var(--mono); margin: 4px 0 0; max-height: 160px; overflow: auto; }
  form { display: flex; gap: 6px; } form input { flex: 1; }
  .list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 4px; }
  .list li { display: flex; gap: 8px; align-items: center; padding: 4px; border-bottom: 1px solid var(--line); }
  .list img { width: 36px; height: 36px; object-fit: contain; background: #fff; border-radius: 4px; }
  .link { background: none; border: 0; padding: 0; color: var(--accent); text-align: left; }
  .card { border: 1px solid var(--line); border-radius: 8px; padding: 8px; }
  .row { display: flex; gap: 8px; align-items: center; justify-content: space-between; }
  .imgs { display: flex; gap: 6px; flex-wrap: wrap; } .imgs img { height: 80px; background: #fff; border-radius: 4px; }
  table { font-size: 12px; border-collapse: collapse; width: 100%; } td { padding: 2px 6px 2px 0; }
  textarea { flex: 1; min-height: 360px; font: 12px/1.4 var(--mono); resize: vertical; white-space: pre; }
  h4 { margin: 8px 0 2px; }
</style>
