<script>
  // Bed view for one plate. Adapted from openscad_viewer's Viewer.svelte (same scene, lights, controls and
  // bed drawing). Here several STLs sit on the bed at their plate transform; objects are picked and
  // dragged on the bed plane; objects outside the printable area turn red. After a slice the view
  // switches to the G-code toolpaths (brim, supports, walls, ...) with a layer slider; Model goes back.
  import { onMount } from 'svelte'
  import * as THREE from 'three'
  import { OrbitControls } from 'three/addons/controls/OrbitControls.js'
  import { STLLoader } from 'three/addons/loaders/STLLoader.js'
  import { api } from './api.js'
  import { bedBox, modelMatrix, transformedBounds, placementMatrix, fitsBed } from './transform.js'
  import { featureColor } from './gcode.js'

  /**
   * bed: config.bed; objects: plate.objects; revs: {modelId: n}, bumped when that model's STL is replaced;
   * selected: object id; fitKey: plate id (the camera re-fits when it changes);
   * onselect(id | null); onmove(oid, x, y) -> Promise, the parent PATCHes the object;
   * gcodeUrl: the current (not stale) slice's G-code, versioned per slice, or null. A new one opens the preview.
   */
  let { bed, objects = [], revs = {}, selected = null, fitKey = '', onselect, onmove, gcodeUrl = null } = $props()

  let host
  let wireframe = $state(false)
  let loaded = $state(0) // bumped when an STL arrives, so the placement effect runs again
  let dragPos = $state(null) // {id, x, y} while an object is being dragged
  let outside = $state([]) // object ids not inside the printable area
  let loadError = $state('')
  let mode = $state('model') // 'model' | 'preview'
  let paths = $state(null) // {layers, features: [{name, count}]} of the loaded G-code
  let pathsState = $state('') // '' | 'loading' | error text
  let layerTop = $state(0)
  let hidden = $state({}) // feature name -> true when switched off

  let renderer, scene, camera, controls, bedGroup, root, raycaster
  let material, matSel, matBad, edgeMat
  let fittedFor = null
  let pathGroup = null // THREE.Group of LineSegments, one per feature
  let pathLines = [] // {name, line, ends}
  let pathsUrl = null
  let worker = null
  let reqId = 0
  let press = null
  const geoms = new Map() // model id -> {rev, data: {geom, edges, center} | null}
  const groups = new Map() // object id -> {group, mesh, geom, key, m0, bounds}
  const ground = new THREE.Plane(new THREE.Vector3(0, 0, 1), 0)

  THREE.Object3D.DEFAULT_UP.set(0, 0, 1)

  function themeColors() {
    const css = getComputedStyle(document.documentElement)
    return {
      bg: css.getPropertyValue('--viewer-bg').trim() || '#f4f4f2',
      grid: css.getPropertyValue('--viewer-grid').trim() || '#d6d5cf',
      plate: css.getPropertyValue('--viewer-plate').trim() || '#e9e8e3',
      model: css.getPropertyValue('--model').trim() || '#e8a33d',
      edge: css.getPropertyValue('--accent').trim() || '#c2410c',
    }
  }

  function applyColors(c) {
    material.color.set(c.model)
    matSel.color.set(c.model).lerp(new THREE.Color(1, 1, 1), 0.4)
  }

  function buildBed() {
    if (bedGroup) scene.remove(bedGroup)
    const c = themeColors()
    const b = bedBox(bed)
    const w = b.x1 - b.x0
    const d = b.y1 - b.y0
    bedGroup = new THREE.Group()
    const plate = new THREE.Mesh(
      new THREE.PlaneGeometry(w, d),
      new THREE.MeshBasicMaterial({ color: c.plate, transparent: true, opacity: 0.6, side: THREE.DoubleSide }),
    )
    plate.position.set((b.x0 + b.x1) / 2, (b.y0 + b.y1) / 2, -0.05)
    bedGroup.add(plate)
    // 10 mm grid lines, from the front-left origin
    const pts = []
    for (let x = b.x0; x <= b.x1 + 1e-6; x += 10) pts.push(x, b.y0, 0, x, b.y1, 0)
    for (let y = b.y0; y <= b.y1 + 1e-6; y += 10) pts.push(b.x0, y, 0, b.x1, y, 0)
    const g = new THREE.BufferGeometry()
    g.setAttribute('position', new THREE.Float32BufferAttribute(pts, 3))
    bedGroup.add(new THREE.LineSegments(g, new THREE.LineBasicMaterial({ color: c.grid })))
    if (b.shape) {
      const outline = new THREE.BufferGeometry().setFromPoints(b.shape.map(([x, y]) => new THREE.Vector3(x, y, 0.05)))
      bedGroup.add(new THREE.LineLoop(outline, new THREE.LineBasicMaterial({ color: c.edge })))
    }
    const axes = new THREE.AxesHelper(20) // the origin, front-left
    axes.position.z = 0.05
    bedGroup.add(axes)
    scene.add(bedGroup)
    scene.background = new THREE.Color(c.bg)
    applyColors(c)
  }

  // Box of everything on the bed, or the bed itself while the plate is empty.
  function contentBox() {
    if (mode === 'preview' && pathGroup) return new THREE.Box3().setFromObject(pathGroup)
    if (root.children.length) {
      root.updateMatrixWorld(true)
      return new THREE.Box3().setFromObject(root)
    }
    const b = bedBox(bed)
    return new THREE.Box3(new THREE.Vector3(b.x0, b.y0, 0), new THREE.Vector3(b.x1, b.y1, 20))
  }

  function fit() {
    const box = contentBox()
    const size = box.getSize(new THREE.Vector3())
    const center = box.getCenter(new THREE.Vector3())
    const radius = Math.max(size.length() / 2, 5)
    const dist = (radius / Math.sin((camera.fov * Math.PI) / 360)) * 1.1
    const dir = new THREE.Vector3(1, -1.4, 0.9).normalize()
    camera.position.copy(center).addScaledVector(dir, dist)
    camera.near = dist / 1000
    camera.far = dist * 100
    camera.updateProjectionMatrix()
    controls.target.copy(center)
    controls.update()
  }

  function view(name) {
    const center = contentBox().getCenter(new THREE.Vector3())
    const dist = camera.position.distanceTo(controls.target)
    const dirs = { top: [0, -0.001, 1], front: [0, -1, 0], right: [1, 0, 0], iso: [1, -1.4, 0.9] }
    camera.position.copy(center).addScaledVector(new THREE.Vector3(...dirs[name]).normalize(), dist)
    controls.target.copy(center)
    controls.update()
  }

  // STL for one model. Replacing a model (re-import) disposes the old buffers; groups rebuild on the new geometry.
  function load(id, rev) {
    const old = geoms.get(id)?.data
    const entry = { rev, data: null }
    geoms.set(id, entry)
    fetch(api.modelUrl(id, rev))
      .then(async (res) => {
        if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
        const geom = new STLLoader().parse(await res.arrayBuffer())
        geom.computeBoundingBox()
        const center = geom.boundingBox.getCenter(new THREE.Vector3())
        const edges = new THREE.EdgesGeometry(geom, 30)
        if (geoms.get(id) !== entry) {
          geom.dispose()
          edges.dispose()
          return
        }
        entry.data = { geom, edges, center }
        if (old) {
          old.geom.dispose()
          old.edges.dispose()
        }
        loaded++
      })
      .catch((e) => {
        if (geoms.get(id) === entry) loadError = `Couldn't load model ${id}: ${e.message || e}`
      })
  }

  function ensureGroup(obj, d) {
    let g = groups.get(obj.id)
    if (g && g.geom === d.geom) return g
    if (g) root.remove(g.group)
    const mesh = new THREE.Mesh(d.geom, material)
    mesh.userData.objId = obj.id
    const group = new THREE.Group()
    group.matrixAutoUpdate = false
    group.add(mesh, new THREE.LineSegments(d.edges, edgeMat))
    root.add(group)
    g = { group, mesh, geom: d.geom, key: null, m0: null, bounds: null }
    groups.set(obj.id, g)
    return g
  }

  // Puts every object on the bed. Runs as an effect, so it re-runs for any change in the plate or the drag.
  function sync(objs, revMap, sel, drag) {
    if (!root) return
    const b = bedBox(bed)
    const seen = new Set()
    const out = []
    for (const obj of objs) {
      const id = obj.model
      const rev = revMap[id] ?? 0
      let entry = geoms.get(id)
      if (!entry || entry.rev !== rev) {
        load(id, rev)
        entry = geoms.get(id)
      }
      seen.add(obj.id)
      if (!entry.data) continue // keep the old group until the new STL arrives
      const d = entry.data
      const g = ensureGroup(obj, d)
      const key = `${obj.rotation.join(',')}|${obj.scale}`
      if (g.key !== key) {
        g.m0 = modelMatrix(d.center, obj.rotation, obj.scale)
        g.bounds = transformedBounds(d.geom, g.m0)
        g.key = key
      }
      const p = drag && drag.id === obj.id ? drag : obj
      g.group.matrix.copy(placementMatrix(g.m0, g.bounds, p.x, p.y))
      g.group.matrixWorldNeedsUpdate = true
      const fits = fitsBed(b, g.bounds, p.x, p.y)
      if (!fits) out.push(obj.id)
      g.mesh.material = !fits ? matBad : obj.id === sel ? matSel : material
    }
    for (const [id, g] of groups) {
      if (seen.has(id)) continue
      root.remove(g.group)
      groups.delete(id)
    }
    outside = out
    if (fittedFor !== fitKey && (groups.size || !objs.length)) {
      fit()
      fittedFor = fitKey
    }
  }

  function setRay(ev) {
    const r = renderer.domElement.getBoundingClientRect()
    const ndc = new THREE.Vector2(((ev.clientX - r.left) / r.width) * 2 - 1, -((ev.clientY - r.top) / r.height) * 2 + 1)
    raycaster.setFromCamera(ndc, camera)
  }

  function pick(ev) {
    setRay(ev)
    const meshes = [...groups.values()].map((g) => g.mesh)
    const hit = raycaster.intersectObjects(meshes, false)[0]
    return hit ? hit.object.userData.objId : null
  }

  function onBed(ev) {
    setRay(ev)
    const p = new THREE.Vector3()
    return raycaster.ray.intersectPlane(ground, p) ? p : null
  }

  const round = (v) => Math.round(v * 10) / 10
  const moved = (ev, p) => Math.hypot(ev.clientX - p.sx, ev.clientY - p.sy) >= 4

  // Capture phase on the host: an object grab stops here, so OrbitControls does not start a rotate.
  function onDown(ev) {
    if (ev.button !== 0 || mode === 'preview') return
    const id = pick(ev)
    if (id === null) {
      press = { id: null, sx: ev.clientX, sy: ev.clientY }
      return
    }
    ev.stopPropagation()
    const obj = objects.find((o) => o.id === id)
    press = { id, sx: ev.clientX, sy: ev.clientY, moved: false, start: onBed(ev), x0: obj.x, y0: obj.y }
    renderer.domElement.setPointerCapture(ev.pointerId)
    onselect?.(id)
  }

  function onMove(ev) {
    if (!press || press.id === null) return
    if (!press.moved && !moved(ev, press)) return
    const p = onBed(ev)
    if (!p || !press.start) return
    press.moved = true
    dragPos = { id: press.id, x: round(press.x0 + p.x - press.start.x), y: round(press.y0 + p.y - press.start.y) }
  }

  async function onUp(ev) {
    const p = press
    press = null
    if (!p) return
    if (p.id === null) {
      if (!moved(ev, p)) onselect?.(null)
      return
    }
    const d = dragPos
    if (!p.moved || !d) return
    try {
      if (d.x !== p.x0 || d.y !== p.y0) await onmove?.(d.id, d.x, d.y)
    } finally {
      dragPos = null
    }
  }

  function clearPaths() {
    if (!pathGroup) return
    scene.remove(pathGroup)
    for (const p of pathLines) {
      p.line.geometry.dispose()
      p.line.material.dispose()
    }
    pathGroup = null
    pathLines = []
    paths = null
  }

  function showPaths(data) {
    clearPaths()
    pathGroup = new THREE.Group()
    for (const f of data.features) {
      const g = new THREE.BufferGeometry()
      g.setAttribute('position', new THREE.BufferAttribute(f.positions, 3))
      const line = new THREE.LineSegments(g, new THREE.LineBasicMaterial({ color: featureColor(f.name) }))
      line.frustumCulled = false
      pathGroup.add(line)
      pathLines.push({ name: f.name, line, ends: f.layerEnds })
    }
    scene.add(pathGroup)
    paths = { layers: data.layers, features: data.features.map((f) => ({ name: f.name, count: f.positions.length / 6 })) }
    layerTop = data.layers.length - 1
  }

  // Loads the G-code in a worker; only the newest request is shown.
  function loadPaths(url) {
    pathsUrl = url
    const id = ++reqId
    pathsState = 'loading'
    worker ??= new Worker(new URL('./gcodeWorker.js', import.meta.url), { type: 'module' })
    worker.onmessage = ({ data }) => {
      if (data.id !== reqId) return
      if (data.error) {
        pathsState = `Couldn't load the toolpaths: ${data.error}`
        return
      }
      pathsState = ''
      showPaths(data)
    }
    worker.postMessage({ id, url })
  }

  onMount(() => {
    renderer = new THREE.WebGLRenderer({ antialias: true })
    renderer.setPixelRatio(window.devicePixelRatio)
    host.appendChild(renderer.domElement)
    scene = new THREE.Scene()
    camera = new THREE.PerspectiveCamera(40, 1, 0.1, 10000)
    controls = new OrbitControls(camera, renderer.domElement)
    controls.enableDamping = true
    raycaster = new THREE.Raycaster()
    root = new THREE.Group()
    scene.add(root)

    const c = themeColors()
    const common = { roughness: 0.55, metalness: 0.05, flatShading: true }
    material = new THREE.MeshStandardMaterial({ color: c.model, ...common })
    matSel = new THREE.MeshStandardMaterial({ color: c.model, ...common })
    matBad = new THREE.MeshStandardMaterial({ color: 0xdc2626, ...common })
    edgeMat = new THREE.LineBasicMaterial({ color: 0x000000, transparent: true, opacity: 0.25 })
    scene.add(new THREE.HemisphereLight(0xffffff, 0x8d8d8d, 1.6))
    const sun = new THREE.DirectionalLight(0xffffff, 1.8)
    sun.position.set(1, -2, 3)
    camera.add(sun) // light follows the camera, so no side is ever black
    scene.add(camera)
    buildBed()
    fit()

    const resize = () => {
      const { clientWidth: w, clientHeight: h } = host
      if (!w || !h) return
      renderer.setSize(w, h)
      camera.aspect = w / h
      camera.updateProjectionMatrix()
    }
    const ro = new ResizeObserver(resize)
    ro.observe(host)
    resize()

    const el = renderer.domElement
    host.addEventListener('pointerdown', onDown, true)
    el.addEventListener('pointermove', onMove)
    el.addEventListener('pointerup', onUp)
    el.addEventListener('pointercancel', onUp)

    const media = matchMedia('(prefers-color-scheme: dark)')
    const onTheme = () => buildBed()
    media.addEventListener('change', onTheme)

    let frame
    const loop = () => {
      controls.update()
      renderer.render(scene, camera)
      frame = requestAnimationFrame(loop)
    }
    loop()
    return () => {
      cancelAnimationFrame(frame)
      ro.disconnect()
      host.removeEventListener('pointerdown', onDown, true)
      el.removeEventListener('pointermove', onMove)
      el.removeEventListener('pointerup', onUp)
      el.removeEventListener('pointercancel', onUp)
      media.removeEventListener('change', onTheme)
      controls.dispose()
      for (const e of geoms.values()) {
        if (!e.data) continue
        e.data.geom.dispose()
        e.data.edges.dispose()
      }
      worker?.terminate()
      clearPaths()
      for (const m of [material, matSel, matBad, edgeMat]) m.dispose()
      renderer.dispose()
    }
  })

  $effect(() => {
    sync(objects, revs, selected, dragPos)
    void loaded // a geometry arrived
  })
  $effect(() => {
    bed // rebuild when the bed size arrives from /api/config
    if (scene) buildBed()
  })
  // A new slice opens the preview; a stale or missing one goes back to the models.
  $effect(() => {
    const url = gcodeUrl
    if (!scene) return
    if (!url) {
      reqId++
      pathsUrl = null
      pathsState = ''
      clearPaths()
      mode = 'model'
    } else if (url !== pathsUrl) {
      loadPaths(url)
      mode = 'preview'
    }
  })
  $effect(() => {
    const preview = mode === 'preview'
    if (!root) return
    root.visible = !preview
    if (pathGroup) pathGroup.visible = preview
    void paths
  })
  $effect(() => {
    const top = layerTop
    const off = hidden
    void paths
    for (const p of pathLines) {
      p.line.visible = !off[p.name]
      p.line.geometry.setDrawRange(0, p.ends[Math.min(top, p.ends.length - 1)] ?? 0)
    }
  })
  $effect(() => {
    const w = wireframe
    if (material) {
      material.wireframe = w
      matSel.wireframe = w
      matBad.wireframe = w
      edgeMat.visible = !w
    }
  })
</script>

<div class="viewer">
  <div class="canvas" bind:this={host}></div>
  <div class="tools">
    <button onclick={() => view('iso')} title="Isometric">Iso</button>
    <button onclick={() => view('front')}>Front</button>
    <button onclick={() => view('right')}>Side</button>
    <button onclick={() => view('top')}>Top</button>
    <button onclick={fit} title="Fit objects in view">Fit</button>
    <span class="seg">
      <button class:on={mode === 'model'} onclick={() => (mode = 'model')}>Model</button>
      <button class:on={mode === 'preview'} disabled={!gcodeUrl} title={gcodeUrl ? 'Toolpaths of the last slice' : 'Slice the plate first'}
        onclick={() => (mode = 'preview')}>Preview</button>
    </span>
    {#if mode === 'model'}
      <label><input type="checkbox" bind:checked={wireframe}> Wireframe</label>
    {:else if pathsState === 'loading'}
      <span class="note">Loading toolpaths…</span>
    {/if}
  </div>
  {#if mode === 'preview' && paths}
    <div class="layers">
      <span class="small">{layerTop + 1}<br>{Number(paths.layers[layerTop]?.z ?? 0).toFixed(2)}</span>
      <input type="range" min="0" max={paths.layers.length - 1} step="1" bind:value={layerTop} aria-label="Top layer" />
      <span class="small muted">{paths.layers.length}</span>
    </div>
    <div class="legend">
      {#each paths.features as f (f.name)}
        <label title="{f.count} segments">
          <input type="checkbox" checked={!hidden[f.name]} onchange={(e) => (hidden = { ...hidden, [f.name]: !e.currentTarget.checked })} />
          <span class="swatch" style:background={featureColor(f.name)}></span>{f.name}
        </label>
      {/each}
    </div>
  {/if}
  {#if mode === 'preview' && pathsState && pathsState !== 'loading'}
    <div class="overlay error">{pathsState}</div>
  {/if}
  {#if !objects.length && mode === 'model'}
    <div class="overlay">Empty plate. Add a model from the library on the left.</div>
  {/if}
  {#if outside.length}
    <div class="warn">{outside.length} object{outside.length > 1 ? 's' : ''} outside the printable area (red)</div>
  {/if}
  {#if loadError}
    <div class="overlay error">{loadError}</div>
  {/if}
</div>

<style>
  .viewer { position: relative; width: 100%; height: 100%; min-height: 0; }
  .canvas { position: absolute; inset: 0; touch-action: none; }
  .canvas :global(canvas) { display: block; }
  .tools {
    position: absolute; top: 8px; left: 8px; display: flex; flex-wrap: wrap; gap: 4px; align-items: center; max-width: calc(100% - 16px);
  }
  .tools button, .tools label {
    font-size: 12px; padding: 3px 8px; background: var(--card); color: var(--text);
    border: 1px solid var(--line); border-radius: 6px; cursor: pointer;
  }
  .tools label { display: flex; gap: 4px; align-items: center; }
  .tools .seg { display: flex; }
  .tools .seg button:first-child { border-radius: 6px 0 0 6px; }
  .tools .seg button:last-child { border-radius: 0 6px 6px 0; border-left: 0; }
  .tools .seg button.on { background: var(--accent); color: var(--on-accent); border-color: var(--accent); }
  .tools button:disabled { opacity: 0.5; cursor: default; }
  .tools .note { font-size: 12px; color: var(--muted); }
  .layers {
    position: absolute; right: 8px; top: 40px; bottom: 8px; display: flex; flex-direction: column; align-items: center; gap: 6px;
    background: var(--card); border: 1px solid var(--line); border-radius: 6px; padding: 6px 4px; text-align: center;
  }
  .layers input { flex: 1; writing-mode: vertical-lr; direction: rtl; width: 20px; min-height: 0; }
  .small { font-size: 11px; line-height: 1.3; }
  .muted { color: var(--muted); }
  .legend {
    position: absolute; right: 52px; bottom: 8px; display: flex; flex-direction: column; gap: 2px; font-size: 12px;
    background: var(--card); color: var(--text); border: 1px solid var(--line); border-radius: 6px; padding: 6px 8px;
    max-height: calc(100% - 56px); overflow: auto;
  }
  .legend label { display: flex; gap: 5px; align-items: center; cursor: pointer; white-space: nowrap; }
  .swatch { width: 10px; height: 10px; border-radius: 2px; flex: none; }
  .warn {
    position: absolute; left: 8px; bottom: 8px; font-size: 12px; padding: 3px 8px; border-radius: 6px;
    background: var(--err-soft); color: var(--err); pointer-events: none;
  }
  .overlay {
    position: absolute; inset: 0; display: grid; place-items: center; pointer-events: none;
    color: var(--muted); font-size: 14px; text-align: center; padding: 16px;
  }
  .overlay.error { color: var(--err); }
</style>
