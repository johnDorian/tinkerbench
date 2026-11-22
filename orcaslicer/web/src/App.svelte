<script>
  import { onMount } from 'svelte'
  import { api } from './lib/api.js'
  import Viewer from './lib/Viewer.svelte'
  import PlatesList from './lib/PlatesList.svelte'
  import ModelsList from './lib/ModelsList.svelte'
  import OpenScadDialog from './lib/OpenScadDialog.svelte'
  import ObjectBar from './lib/ObjectBar.svelte'
  import SettingsPanel from './lib/SettingsPanel.svelte'
  import SlicePanel from './lib/SlicePanel.svelte'
  import PrinterChip from './lib/PrinterChip.svelte'

  let config = $state({
    bed: { width: 250, depth: 250, max_height: 260, shape: null },
    orca_version: '',
    printer_enabled: false,
    webcam_url: '',
    curated_settings: [],
  })
  let plates = $state([])
  let currentId = $state(decodeURIComponent(location.hash.slice(2)))
  let plate = $state(null) // the open Plate
  let profiles = $state({ machines: [], processes: [], filaments: [] })
  let presets = $state({ process: {}, filament: {} }) // flattened process and filament presets of the plate
  let models = $state([])
  let revs = $state({}) // model id -> counter; bumped when its STL is replaced, so the viewer reloads it
  let slice = $state(null)
  let printer = $state(null)
  let selected = $state(null) // object id
  let error = $state('')
  let notice = $state('')
  let working = $state('')
  let connected = $state(false)
  let openscadOpen = $state(false)

  let lastMachine = null
  let lastPresets = null

  const selectedObj = $derived(plate?.objects.find((o) => o.id === selected) ?? null)
  const selectedModel = $derived(models.find((m) => m.id === selectedObj?.model) ?? null)

  // Runs a request; on failure shows the message in the error bar and returns undefined.
  async function guard(fn) {
    try {
      error = ''
      return await fn()
    } catch (e) {
      error = e.message
      return undefined
    }
  }

  const bumped = (id) => ({ ...revs, [id]: (revs[id] ?? 0) + 1 })

  async function loadConfig() {
    config = (await guard(api.config)) ?? config
  }
  async function loadPlates() {
    plates = (await guard(api.plates)) ?? plates
  }
  async function loadModels() {
    models = (await guard(api.models)) ?? models
  }
  function loadPrinter() {
    api.printer().then((p) => (printer = p)).catch(() => {})
  }
  async function loadProfiles(machine) {
    profiles = (await guard(() => api.profiles(machine))) ?? profiles
  }
  async function loadPresets() {
    const p = plate
    if (!p) return
    const fail = (e) => {
      error = e.message
      return {}
    }
    const [process, filament] = await Promise.all([
      api.preset('process', p.process).catch(fail),
      api.preset('filament', p.filament).catch(fail),
    ])
    if (plate?.id === p.id) presets = { process, filament }
  }
  async function loadSlice(id = currentId) {
    if (!id) return
    try {
      const s = await api.sliceResult(id)
      if (id === currentId) slice = s
    } catch (e) {
      if (id !== currentId) return
      if (e.status === 404) slice = null
      else error = e.message
    }
  }

  // Every plate the server sends goes through here: profiles and presets follow the plate's machine and presets.
  function applyPlate(p) {
    if (p.id !== currentId) return
    plate = p
    if (p.machine !== lastMachine) {
      lastMachine = p.machine
      loadProfiles(p.machine)
    }
    const key = `${p.process}\n${p.filament}`
    if (key !== lastPresets) {
      lastPresets = key
      loadPresets()
    }
    loadSlice(p.id)
  }

  async function loadPlate(id = currentId) {
    if (!id) return
    const p = await guard(() => api.plate(id))
    if (p) applyPlate(p)
  }

  // Save the whole plate (PUT); the server keeps id and created.
  async function saveWith(patch) {
    if (!plate) return
    const next = { ...$state.snapshot(plate), ...patch }
    const saved = await guard(() => api.savePlate(next))
    if (saved) applyPlate(saved)
    loadPlates()
    return saved
  }

  function openPlate(id) {
    currentId = id
    selected = null
    plate = null
    slice = null
    lastMachine = null
    lastPresets = null
    history.replaceState(null, '', id ? '#/' + encodeURIComponent(id) : location.pathname)
    loadPlate(id)
  }

  async function newPlate() {
    const name = prompt('Plate name:')?.trim()
    if (!name) return
    const p = await guard(() => api.createPlate({ name }))
    if (!p) return
    await loadPlates()
    openPlate(p.id)
  }

  async function deletePlate() {
    if (!plate || !confirm(`Delete plate "${plate.name}"? Its models stay in the library.`)) return
    const id = plate.id
    if ((await guard(() => api.deletePlate(id))) === undefined) return
    await loadPlates()
    openPlate(plates[0]?.id ?? '')
  }

  async function setProfile(kind, value) {
    if (!plate) return
    const patch = { [kind]: value }
    if (kind === 'machine') {
      const prof = await guard(() => api.profiles(value))
      if (!prof) return
      profiles = prof
      lastMachine = value
      // keep the presets when they still exist for the new printer, otherwise take the first compatible one
      if (!prof.processes.some((x) => x.name === plate.process)) patch.process = prof.processes[0]?.name ?? plate.process
      if (!prof.filaments.some((x) => x.name === plate.filament)) patch.filament = prof.filaments[0]?.name ?? plate.filament
    }
    await saveWith(patch)
  }

  // Override = a curated key set on the plate; undefined removes it (back to the preset).
  function setOverride(key, value) {
    if (!plate) return
    const overrides = { ...(plate.overrides ?? {}) }
    if (value === undefined) delete overrides[key]
    else overrides[key] = value
    saveWith({ overrides })
  }

  async function addModel(modelId) {
    if (!plate) return
    const pid = plate.id
    const before = new Set(plate.objects.map((o) => o.id))
    const p = await guard(() => api.addObject(pid, { model: modelId }))
    if (!p) return
    selected = p.objects.find((o) => !before.has(o.id))?.id ?? null
    applyPlate(p)
  }

  async function patchObject(oid, body) {
    if (!plate) return
    const pid = plate.id
    const p = await guard(() => api.patchObject(pid, oid, body))
    if (p) applyPlate(p)
  }

  async function duplicate(oid) {
    if (!plate) return
    const pid = plate.id
    const before = new Set(plate.objects.map((o) => o.id))
    const p = await guard(() => api.duplicateObject(pid, oid))
    if (!p) return
    selected = p.objects.find((o) => !before.has(o.id))?.id ?? selected
    applyPlate(p)
  }

  async function removeObject(oid) {
    if (!plate) return
    const pid = plate.id
    const p = await guard(() => api.removeObject(pid, oid))
    if (!p) return
    selected = null
    applyPlate(p)
  }

  async function arrange() {
    if (!plate) return
    const p = await guard(() => api.arrange(plate.id, 6))
    if (p) applyPlate(p)
  }

  async function deleteModel(m) {
    if (!confirm(`Delete model "${m.name}" from the library?`)) return
    if ((await guard(() => api.deleteModel(m.id))) === undefined) return
    loadModels()
  }

  async function uploadFiles(files) {
    for (const f of files) {
      if (!/\.stl$/i.test(f.name)) {
        error = `${f.name}: only .stl files can be uploaded`
        continue
      }
      working = `Uploading ${f.name}…`
      await guard(() => api.uploadModel(f))
    }
    working = ''
    loadModels()
  }

  // Re-importing replaces the STL in place (same id), so the viewer reloads it and plates get marked stale.
  async function importOpenscad(project) {
    working = `Importing ${project} from OpenSCAD…`
    try {
      const m = await guard(() => api.importOpenscad(project))
      if (!m) return undefined
      revs = bumped(m.id)
      await loadModels()
      loadPlates()
      return m
    } finally {
      working = ''
    }
  }

  // ?import=<project>: import the project's model, open the plate named after it, add the model if missing.
  async function importAndOpen(project) {
    const m = await importOpenscad(project)
    if (!m) return
    await loadPlates()
    const found = plates.find((p) => p.name === project)
    let p = await guard(() => (found ? api.plate(found.id) : api.createPlate({ name: project })))
    if (!p) return
    if (!p.objects.some((o) => o.model === m.id)) p = await guard(() => api.addObject(p.id, { model: m.id })) ?? p
    openPlate(p.id)
    loadPlates()
  }

  async function printPlate() {
    if (!plate) return undefined
    const pid = plate.id
    const r = await guard(() => api.print(pid))
    if (r) {
      notice = `Sent ${r.filename} to the printer.`
      setTimeout(() => (notice = ''), 8000)
      loadPrinter()
    }
    return r
  }

  async function sliceNow() {
    if (!plate) return
    const pid = plate.id
    const s = await guard(() => api.slice(pid))
    if (s && pid === currentId) slice = s
  }

  async function printerAction(action) {
    await guard(() => api.printerAction(action))
    loadPrinter()
  }

  function onEvent(e) {
    switch (e.type) {
      case 'plate':
        loadPlates()
        if (e.id === currentId) loadPlate(currentId)
        break
      case 'slice':
        if (e.id === currentId) loadSlice(currentId)
        break
      case 'models':
        if (e.id) revs = bumped(e.id)
        else revs = Object.fromEntries(models.map((m) => [m.id, (revs[m.id] ?? 0) + 1]))
        loadModels()
        break
      case 'printer':
        if ('state' in e) printer = e
        else loadPrinter()
        break
    }
  }

  function refreshAll() {
    loadPlates()
    loadModels()
    loadPrinter()
    if (currentId) loadPlate(currentId)
  }

  // G-code of the current slice for the viewer's toolpath preview; null while slicing or once the plate changed.
  const previewUrl = $derived(
    plate && slice?.status === 'done' && !slice.stale && slice.gcode_name
      ? `${api.gcodeUrl(plate.id)}?v=${encodeURIComponent(slice.finished || slice.gcode_name)}`
      : null,
  )

  // While a slice runs, poll as well as listening for events, so a missed event cannot leave it "slicing".
  $effect(() => {
    const st = slice?.status
    if (st !== 'queued' && st !== 'slicing') return
    const t = setInterval(() => loadSlice(currentId), 3000)
    return () => clearInterval(t)
  })

  onMount(() => {
    const imp = new URLSearchParams(location.search).get('import')
    loadConfig()
    loadPlates()
    loadModels()
    loadPrinter()
    if (currentId) loadPlate(currentId)
    if (imp) {
      history.replaceState(null, '', location.pathname + location.hash)
      importAndOpen(imp)
    }

    const es = new EventSource('/api/events')
    es.onopen = () => {
      // catch up on anything missed while disconnected
      if (!connected) refreshAll()
      connected = true
    }
    es.onerror = () => (connected = false)
    es.onmessage = (m) => onEvent(JSON.parse(m.data))
    const onHash = () => openPlate(decodeURIComponent(location.hash.slice(2)))
    addEventListener('hashchange', onHash)
    return () => {
      es.close()
      removeEventListener('hashchange', onHash)
    }
  })
</script>

<div class="app">
  <header class="topbar">
    <div class="brand">
      <svg viewBox="0 0 32 32" aria-hidden="true">
        <rect width="32" height="32" rx="7" style="fill: var(--accent)" />
        <path d="M9 22h14M11 22v-8h10v8M13 14V9h6v5" fill="none" style="stroke: var(--on-accent)" stroke-width="2" stroke-linejoin="round" />
      </svg>
      <span class="brand-name">OrcaSlicer <span class="brand-sub">Workbench</span></span>
    </div>

    <div class="group">
      <select class="plate-select" value={currentId} aria-label="Open plate" onchange={(e) => openPlate(e.currentTarget.value)}>
        {#if !plates.some((p) => p.id === currentId)}<option value="" disabled>Choose a plate…</option>{/if}
        {#each plates as p (p.id)}<option value={p.id}>{p.name}</option>{/each}
      </select>
      <button onclick={newPlate}>+ New</button>
    </div>

    <span class="spacer"></span>

    {#if config.orca_version}<div class="status">{config.orca_version}</div>{/if}
    <PrinterChip {printer} webcamUrl={config.webcam_url} printerEnabled={config.printer_enabled} onaction={printerAction} />
    <span class="live" class:on={connected} title={connected ? 'Live updates' : 'Reconnecting…'}>{connected ? 'Live' : 'Offline'}</span>
  </header>

  {#if error}
    <div class="banner err">{error}<button class="close" onclick={() => (error = '')} aria-label="Dismiss">×</button></div>
  {/if}
  {#if working}
    <div class="banner warn">{working}</div>
  {/if}
  {#if notice}
    <div class="banner ok">{notice}</div>
  {/if}

  <main class="workspace with-left">
    <aside class="panel side-left">
      <div class="panel-body">
        <PlatesList {plates} current={currentId} onopen={openPlate} onnew={newPlate} />
        <ModelsList
          {models}
          canAdd={!!plate}
          onadd={addModel}
          ondelete={deleteModel}
          onupload={uploadFiles}
          onimport={() => (openscadOpen = true)}
          onreimport={(m) => importOpenscad(m.openscad_project)}
        />
      </div>
    </aside>

    <section class="panel stage">
      {#if plate}
        <div class="panel-bar">
          <input
            class="plate-name"
            value={plate.name}
            aria-label="Plate name"
            onchange={(e) => {
              const name = e.currentTarget.value.trim()
              if (name && name !== plate.name) saveWith({ name })
              else e.currentTarget.value = plate.name
            }}
          />
          <span class="muted small">{plate.objects.length} object{plate.objects.length === 1 ? '' : 's'}</span>
          <span class="spacer"></span>
          <button onclick={arrange} disabled={!plate.objects.length}>Arrange</button>
          <button class="danger" onclick={deletePlate}>Delete plate</button>
        </div>
        <ObjectBar
          object={selectedObj}
          model={selectedModel}
          onpatch={patchObject}
          onduplicate={duplicate}
          ondelete={removeObject}
        />
      {:else}
        <div class="panel-bar">
          <span class="muted small">{currentId ? 'Loading plate…' : 'No plate open. Create one on the left, or press Slice in the OpenSCAD workbench.'}</span>
        </div>
      {/if}
      <!-- kept mounted across plate switches: one WebGL context for the page -->
      <div class="viewer-wrap">
        <Viewer
          bed={config.bed}
          objects={plate?.objects ?? []}
          {revs}
          {selected}
          fitKey={plate?.id ?? ''}
          onselect={(id) => (selected = id)}
          onmove={(oid, x, y) => patchObject(oid, { x, y })}
          gcodeUrl={previewUrl}
        />
      </div>
    </section>

    <aside class="panel side">
      {#if plate}
        <div class="panel-body">
          <SettingsPanel
            {plate}
            {profiles}
            {presets}
            curated={config.curated_settings ?? []}
            beds={config.beds ?? []}
            onprofile={setProfile}
            onoverride={setOverride}
          />
          <SlicePanel
            {plate}
            {slice}
            {printer}
            printerEnabled={config.printer_enabled}
            onslice={sliceNow}
            onprint={printPlate}
          />
        </div>
      {/if}
    </aside>
  </main>
</div>

{#if openscadOpen}
  <OpenScadDialog {models} onclose={() => (openscadOpen = false)} onimport={importOpenscad} />
{/if}

<style>
  /* the grid's one row fills the page; the stage gets the height, the sidebars scroll inside */
  main.workspace { grid-template-rows: minmax(0, 1fr); }
  .plate-select { max-width: 240px; }
  .plate-name {
    font: inherit; font-size: 15px; font-weight: 600; border: 1px solid transparent; background: none; color: var(--text);
    padding: 2px 4px; border-radius: var(--radius-sm); min-width: 0; width: min(320px, 100%);
  }
  .plate-name:hover, .plate-name:focus { border-color: var(--line); background: var(--card); outline: none; }
  .stage { min-width: 0; }
  .viewer-wrap { flex: 1; min-height: 360px; position: relative; }

  @media (max-width: 1100px) {
    main.workspace { grid-template-rows: auto; }
    .workspace > .side-left { max-height: 70vh; }
  }
  @media (max-width: 720px) {
    .workspace > .side-left { max-height: 420px; }
  }
</style>
