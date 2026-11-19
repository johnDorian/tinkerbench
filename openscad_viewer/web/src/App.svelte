<script>
  import { onMount } from 'svelte'
  import { api, errorLines, ago, who } from './lib/api.js'
  import Viewer from './lib/Viewer.svelte'
  import CodePane from './lib/CodePane.svelte'
  import RenderPanel from './lib/RenderPanel.svelte'
  import History from './lib/History.svelte'
  import CommentThread from './lib/CommentThread.svelte'
  import Images from './lib/Images.svelte'
  import Measure from './lib/Measure.svelte'

  let config = $state({ bed: [220, 220], openscad_version: '', mcp_url: '', slicer_url: '' })
  let projects = $state([])
  let current = $state(decodeURIComponent(location.hash.slice(2)) || '')
  let project = $state(null) // GET /api/projects/{p}
  let selectedFile = $state('main.scad')
  let rendering = $state(false)
  let stlVersion = $state(0) // bumped after each render so the viewer reloads the STL
  let partsVersion = $state(0) // bumped when the per-part renders finish (parts_done)
  let viewer = $state.raw() // the Viewer component: showComment() for the "show in 3D" buttons
  let changedFiles = $state(new Set())
  let error = $state('')
  let connected = $state(false)
  let activity = $state([]) // recent events, newest first
  let side = $state('activity') // activity | comments | history
  let focus = $state(null) // {file, line, n}: scroll the code pane there
  let showResolved = $state(false)
  let versions = $state([])
  let preview = $state(null) // {n, label, content}
  let images = $state([]) // uploaded scans and photos of the real part
  let measuring = $state(null) // the image open in the measuring dialog
  let follow = $state(loadPref('follow', true))

  function loadPref(k, def) {
    try {
      const v = localStorage.getItem('openscad.' + k)
      return v === null ? def : JSON.parse(v)
    } catch {
      return def
    }
  }
  $effect(() => {
    try { localStorage.setItem('openscad.follow', JSON.stringify(follow)) } catch {}
  })

  const marks = $derived(errorLines([...(project?.last_render?.errors ?? []), ...(project?.last_render?.warnings ?? [])]))
  const openComments = $derived((project?.comments ?? []).filter((c) => !c.resolved))
  const listedComments = $derived(showResolved ? project?.comments ?? [] : openComments)

  const commentActions = {
    add: (c) => guard(() => api.addComment(current, c)),
    reply: (id, body) => guard(() => api.reply(current, id, body)),
    resolve: (id, resolved) => guard(() => api.resolve(current, id, resolved)),
    apply: (id) => guard(() => api.applySuggestion(current, id)),
    remove: (id) => guard(() => api.deleteComment(current, id)),
  }

  // Reveal a source line (comment, part or feature). Model-only items have no file and are ignored.
  function goTo(c) {
    if (!c?.file || !project?.files?.includes(c.file)) return
    preview = null
    selectedFile = c.file
    focus = { file: c.file, line: c.line, n: (focus?.n ?? 0) + 1 }
  }

  const stlUrl = $derived(project && project.last_render?.stats ? `${api.stlUrl(project.name)}?v=${stlVersion}` : null)

  async function guard(fn) {
    try {
      error = ''
      return await fn()
    } catch (e) {
      error = e.message
    }
  }

  async function loadProjects() {
    projects = (await guard(api.projects)) ?? projects
    if (!current && projects.length) select(projects[0].name)
  }

  async function loadProject() {
    if (!current) {
      project = null
      return
    }
    const name = current
    try {
      const p = await api.project(name)
      if (name !== current) return
      project = p
      if (!p.files.includes(selectedFile)) selectedFile = p.files[0]
    } catch (e) {
      if (name === current) {
        project = null
        error = e.message
      }
    }
    if (side === 'history') loadHistory()
    loadImages()
  }

  async function loadImages() {
    images = current ? ((await guard(() => api.images(current))) ?? []) : []
  }

  async function loadHistory() {
    if (current) versions = (await guard(() => api.history(current))) ?? []
  }

  function select(name) {
    if (name === current) return
    current = name
    preview = null
    measuring = null
    images = []
    rendering = false
    selectedFile = 'main.scad'
    history.replaceState(null, '', name ? '#/' + encodeURIComponent(name) : '#')
    loadProject()
  }

  function flash(file) {
    changedFiles = new Set([...changedFiles, file])
    setTimeout(() => {
      changedFiles.delete(file)
      changedFiles = new Set(changedFiles)
    }, 1500)
  }

  function onEvent(e) {
    activity = [e, ...activity].slice(0, 50)
    if (e.kind === 'project_created' || e.kind === 'project_deleted') loadProjects()
    // parts_done is background work on a project; it does not pull the view over to that project.
    if (follow && e.source !== 'web' && e.project !== current && e.kind !== 'project_deleted' && e.kind !== 'parts_done') {
      select(e.project)
      return
    }
    if (e.project !== current) return
    switch (e.kind) {
      case 'project_deleted':
        select('')
        break
      case 'file_changed':
        if (e.source !== 'web' && follow) selectedFile = e.file
        flash(e.file)
        loadProject()
        break
      case 'project_updated':
      case 'comments_changed':
        loadProject()
        break
      case 'images_changed':
        loadImages()
        break
      case 'render_started':
        rendering = true
        break
      case 'render_done':
        rendering = false
        stlVersion++
        loadProject()
        break
      case 'parts_done':
        partsVersion++
        break
    }
  }

  onMount(() => {
    guard(async () => (config = await api.config()))
    loadProjects()
    loadProject()
    const es = new EventSource('/api/events')
    es.onopen = () => {
      // catch up on anything missed while disconnected
      if (!connected) { loadProjects(); loadProject() }
      connected = true
    }
    es.onerror = () => (connected = false)
    es.onmessage = (m) => onEvent(JSON.parse(m.data))
    const onHash = () => select(decodeURIComponent(location.hash.slice(2)))
    addEventListener('hashchange', onHash)
    return () => {
      es.close()
      removeEventListener('hashchange', onHash)
    }
  })

  async function newProject() {
    const name = prompt('Project name (lowercase letters, digits, - and _):')?.trim()
    if (!name) return
    const description = prompt('What is it for? (optional)') ?? ''
    if (await guard(() => api.create(name, description))) {
      await loadProjects()
      select(name)
    }
  }

  async function deleteProject() {
    if (!confirm(`Delete project "${current}"? It is moved to the trash folder on the server.`)) return
    if ((await guard(() => api.remove(current))) !== undefined) {
      current = ''
      project = null
      await loadProjects()
    }
  }

  const save = (file, content) => guard(() => api.saveFile(current, file, content))

  function rerender(rawParams) {
    rendering = true
    guard(() => api.render(current, rawParams)).finally(() => (rendering = false))
  }

  async function pickVersion(v) {
    const res = await guard(() => api.version(current, v.n))
    if (!res) return
    selectedFile = v.file
    preview = { n: v.n, content: res.content, label: `Version #${v.n} of ${v.file} · ${v.source || '?'} · ${ago(v.time)}${v.note ? ' · ' + v.note : ''}` }
  }

  async function restore() {
    if (await guard(() => api.restore(current, preview.n))) preview = null
  }

  function showSide(s) {
    side = s
    if (s === 'history') loadHistory()
  }

  function describe(e) {
    const by = who(e.source)
    switch (e.kind) {
      case 'file_changed': return `${by} saved ${e.file}`
      case 'render_started': return `${by} started a render`
      case 'render_done': return e.ok ? 'render finished' : 'render failed'
      case 'project_created': return `${by} created the project`
      case 'project_deleted': return `${by} deleted the project`
      case 'project_updated': return `${by} changed description/params`
      case 'comments_changed': return `${by} ${e.detail}`
      case 'images_changed': return `${by} ${e.detail}`
      case 'parts_done': return e.ok ? 'parts labelled' : 'labelling parts failed'
    }
    return e.kind
  }
</script>

<header>
  <div class="brand">OpenSCAD Workbench</div>
  <select value={current} onchange={(e) => select(e.currentTarget.value)}>
    {#if !current}<option value="">Choose a project…</option>{/if}
    {#each projects as p (p.name)}<option value={p.name}>{p.name}</option>{/each}
  </select>
  <button onclick={newProject}>New</button>
  {#if project}
    <a class="button" href="{api.stlUrl(project.name)}?download" download>Download STL</a>
    {#if project.last_render?.stats && config.slicer_url}
      <a class="button" href={`${config.slicer_url}/?import=${encodeURIComponent(project.name)}`} target="_blank" rel="noopener" title="Open this model in the slicer (new tab)">Slice</a>
    {/if}
    <button class="danger" onclick={deleteProject}>Delete</button>
  {/if}
  <span class="spacer"></span>
  <label class="follow" title="Jump to whatever the agent is working on">
    <input type="checkbox" bind:checked={follow}> Follow agent
  </label>
  <span class="conn" class:on={connected} title={connected ? 'Live' : 'Reconnecting…'}></span>
</header>

{#if error}
  <div class="error-bar">{error} <button onclick={() => (error = '')}>×</button></div>
{/if}

{#if project}
  {#if project.description}<div class="description">{project.description}</div>{/if}
  <main>
    <section class="code card">
      <CodePane
        files={project.content}
        bind:selected={selectedFile}
        {marks}
        changed={changedFiles}
        {preview}
        onsave={save}
        onclosepreview={() => (preview = null)}
        onrestore={restore}
        comments={project.comments}
        on={commentActions}
        onshow={(c) => viewer?.showComment(c)}
        {focus}
      />
    </section>
    <section class="model card">
      <div class="viewer-wrap">
        <Viewer
          bind:this={viewer}
          url={stlUrl}
          fitKey={project.name}
          bed={config.bed}
          stlTime={project.last_render?.stl_time ?? null}
          renderVersion={stlVersion}
          {partsVersion}
          comments={project.comments ?? []}
          on={commentActions}
          onjump={goTo}
        />
      </div>
      <div class="render">
        <RenderPanel result={project.last_render} params={project.params} {rendering} bed={config.bed} onrender={rerender} />
      </div>
    </section>
    <aside class="card">
      <div class="side-tabs">
        <button class:active={side === 'activity'} onclick={() => showSide('activity')}>Activity</button>
        <button class:active={side === 'comments'} onclick={() => showSide('comments')}>
          Comments{#if openComments.length}<span class="badge">{openComments.length}</span>{/if}
        </button>
        <button class:active={side === 'images'} onclick={() => showSide('images')}>
          Images{#if images.length}<span class="badge count">{images.length}</span>{/if}
        </button>
        <button class:active={side === 'history'} onclick={() => showSide('history')}>History</button>
      </div>
      {#if side === 'history'}
        <History {versions} active={preview?.n} onpick={pickVersion} />
      {:else if side === 'images'}
        <Images project={current} {images} onchanged={loadImages} onmeasure={(img) => (measuring = img)} />
      {:else if side === 'comments'}
        <div class="comments">
          <label class="muted"><input type="checkbox" bind:checked={showResolved}> Show resolved</label>
          {#each listedComments as c (c.id)}
            {#if c.file}
              <button class="jump" onclick={() => goTo(c)}>{c.file}:{c.line}</button>
            {:else}
              <button class="jump" onclick={() => viewer?.showComment(c)}>On the model · {c.anchor?.part || 'whole model'}</button>
            {/if}
            <CommentThread comment={c} on={commentActions} onshow={(x) => viewer?.showComment(x)} compact />
          {:else}
            <p class="muted">Click a line number in the code to comment on it. Then ask the agent to "address my comments".</p>
          {/each}
        </div>
      {:else}
        <ol class="activity">
          {#each activity.filter((e) => e.project === current) as e}
            <li><span>{describe(e)}</span><time>{ago(e.time)}</time></li>
          {:else}
            <li class="muted">Changes from the agent show up here as they happen.</li>
          {/each}
        </ol>
      {/if}
    </aside>
  </main>
  <Measure project={current} image={measuring} onclose={() => (measuring = null)} />
{:else}
  <div class="intro">
    <h1>No project selected</h1>
    <p>Ask your agent to create one. Connect it to the MCP server at <code>{config.mcp_url || '/mcp'}</code>; with "Follow agent" on, this page switches to whatever it works on.</p>
    <p class="muted">{config.openscad_version}</p>
  </div>
{/if}

<style>
  header {
    display: flex; gap: 8px; align-items: center; padding: 8px 16px; border-bottom: 1px solid var(--line);
    background: var(--card); flex-wrap: wrap;
  }
  .brand { font-weight: 700; margin-right: 8px; }
  .spacer { flex: 1; }
  .follow { display: flex; gap: 6px; align-items: center; font-size: 13px; color: var(--muted); }
  .conn { width: 9px; height: 9px; border-radius: 50%; background: var(--err); }
  .conn.on { background: var(--ok); }
  .error-bar {
    background: var(--err-soft); color: var(--err); padding: 6px 16px; font-size: 13px; display: flex; justify-content: space-between;
  }
  .error-bar button { background: none; border: 0; color: inherit; cursor: pointer; font-size: 16px; }
  .description { padding: 8px 16px 0; color: var(--muted); font-size: 14px; }
  main {
    display: grid; grid-template-columns: minmax(0, 5fr) minmax(0, 6fr) 290px; gap: 12px; padding: 12px 16px;
    height: calc(100dvh - 70px); box-sizing: border-box;
  }
  .card { background: var(--card); border: 1px solid var(--line); border-radius: 10px; overflow: hidden; min-height: 0; }
  .model { display: flex; flex-direction: column; }
  .viewer-wrap { flex: 1; min-height: 240px; }
  .render { border-top: 1px solid var(--line); max-height: 40%; overflow: auto; flex-shrink: 0; }
  aside { display: flex; flex-direction: column; }
  .side-tabs { display: flex; border-bottom: 1px solid var(--line); }
  .side-tabs button {
    flex: 1; background: none; border: 0; border-radius: 0; padding: 8px 4px; color: var(--muted); cursor: pointer;
    font-size: 12px; white-space: nowrap;
  }
  .side-tabs button.active { color: var(--text); box-shadow: inset 0 -2px var(--accent); }
  .badge {
    font-size: 11px; background: var(--accent); color: #fff; border-radius: 999px; padding: 0 6px; margin-left: 4px;
  }
  .badge.count { background: var(--line); color: var(--muted); }
  .comments { overflow: auto; padding: 8px; display: flex; flex-direction: column; gap: 4px; font-size: 13px; }
  .comments label { display: flex; gap: 6px; align-items: center; margin-bottom: 4px; }
  .jump {
    align-self: flex-start; background: none; border: 0; padding: 4px 0 0; color: var(--accent);
    font: 12px var(--mono); cursor: pointer;
  }
  .activity { list-style: none; margin: 0; padding: 8px 12px; overflow: auto; font-size: 13px; }
  .activity li { display: flex; justify-content: space-between; gap: 8px; padding: 4px 0; border-bottom: 1px solid var(--line); }
  .activity time { color: var(--faint); font-size: 12px; white-space: nowrap; }
  .muted { color: var(--muted); }
  .intro { max-width: 560px; margin: 15vh auto; padding: 0 16px; }
  .intro h1 { font-size: 22px; }

  @media (max-width: 1100px) {
    main { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); height: auto; }
    .code, .model { height: 70vh; }
    aside { grid-column: 1 / -1; max-height: 300px; }
  }
  @media (max-width: 720px) {
    main { grid-template-columns: minmax(0, 1fr); }
    .model { order: -1; }
  }
</style>
