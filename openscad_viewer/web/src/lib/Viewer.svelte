<script>
  // three.js view of the model on a print bed. OpenSCAD is Z-up, so the scene is too.
  // Shows model.stl, or one mesh per labelled part once /parts describes the current render.
  // Part names, feature pins and comment pins are CSS2D overlays. Comment mode picks an edge
  // or a face of the meshes (parts.js does the maths) and drops an anchored comment.
  import { onMount, untrack } from 'svelte'
  import * as THREE from 'three'
  import { OrbitControls } from 'three/addons/controls/OrbitControls.js'
  import { STLLoader } from 'three/addons/loaders/STLLoader.js'
  import { CSS2DRenderer, CSS2DObject } from 'three/addons/renderers/CSS2DRenderer.js'
  import { LineSegments2 } from 'three/addons/lines/LineSegments2.js'
  import { LineSegmentsGeometry } from 'three/addons/lines/LineSegmentsGeometry.js'
  import { LineMaterial } from 'three/addons/lines/LineMaterial.js'
  import { api } from './api.js'
  import {
    EDGE_PICK_PX, sameTime, labelPoint, buildTopology, growRegion, regionPositions, nearestEdge, partTints,
  } from './parts.js'
  import PartsList from './PartsList.svelte'
  import CommentThread from './CommentThread.svelte'

  /**
   * url: model.stl to show (changes on every render); fitKey: the project (the camera re-fits when it
   * changes); bed: [x, y] mm; stlTime: stl_time of the last render; renderVersion / partsVersion: bumped
   * on render_done / parts_done; comments: the project's comments (anchored ones get pins);
   * on: comment actions {add, reply, resolve, apply, remove}; onjump({file, line}) reveals source.
   */
  let {
    url = null, fitKey = '', bed = [220, 220], stlTime = null, renderVersion = 0, partsVersion = 0,
    comments = [], on = {}, onjump = () => {},
  } = $props()

  let host
  let ready = $state(false)
  let wireframe = $state(false)
  let labelsOn = $state(true)
  let commentMode = $state(false)
  let empty = $state(true)
  let loadError = $state('')
  let partsInfo = $state(null) // GET /parts for this project; null when there are no parts
  let hidden = $state.raw(new Set())
  let hover3d = $state(null) // part under the cursor in the 3D view
  let hoverList = $state(null) // part hovered in the parts list
  const hoverPart = $derived(hoverList ?? hover3d)
  let itemsVersion = $state(0)
  let hintText = $state('')
  let composer = $state.raw(null) // {x, y, pick, view}: the click that opened the composer
  let composerText = $state('')
  let saving = $state(false)
  let openId = $state(null) // comment whose thread popover is open

  // Parts payload: "current" means it describes the render on screen.
  // model_time is null until a render has produced parts; features are there as soon as the main
  // render is, so they are shown even while the per-part renders are still pending.
  const featuresCurrent = $derived(!!partsInfo && sameTime(partsInfo.model_time, stlTime))
  const partsCurrent = $derived(featuresCurrent && !partsInfo.pending)
  const hasUnlabelled = $derived(partsCurrent && partsInfo.parts.some((p) => p.name === '(unlabelled)'))
  // Per-part meshes only when every part is ready and nothing is left unlabelled: the remainder
  // "(unlabelled)" is plain geometry that every part's STL also contains, so the meshes would overlap.
  // Empty parts (triangles 0) have no mesh; a part with bbox null or an error is not ready.
  const meshParts = $derived(partsCurrent ? partsInfo.parts.filter((p) => p.triangles > 0) : [])
  const usable = $derived(
    partsCurrent && !hasUnlabelled && meshParts.length > 0
      && partsInfo.parts.every((p) => p.ok && !p.error && p.bbox),
  )
  const listParts = $derived(partsCurrent ? partsInfo.parts : [])
  const labelParts = $derived(partsCurrent ? partsInfo.parts.filter((p) => p.bbox && p.triangles > 0) : [])
  const shownFeatures = $derived(featuresCurrent ? partsInfo.features ?? [] : [])
  const pending = $derived(!!partsInfo?.pending)
  const openComment = $derived(openId == null ? null : comments.find((c) => c.id === openId && c.anchor) ?? null)

  // What the meshes should show. Keyed, so a plan that is already on screen is not rebuilt.
  const plan = $derived.by(() => {
    if (usable) {
      const names = meshParts.map((p) => p.name)
      return { key: `parts|${fitKey}|${partsInfo.model_time}|${names.join('\n')}`, parts: names, fallback: url }
    }
    if (url) return { key: `model|${url}`, parts: null, fallback: url }
    return { key: 'none', parts: null, fallback: null }
  })

  THREE.Object3D.DEFAULT_UP.set(0, 0, 1)

  // Non-reactive scene state.
  let renderer, labelRenderer, scene, camera, controls, frame, modelGroup, bedGroup
  let items = []
  let appliedKey = null
  let applyGen = 0
  let partsGen = 0
  let fittedFor = null
  let projectName = null
  let size = { w: 1, h: 1 }
  let overlays = [] // CSS2DObjects for part names, feature pins and comment pins
  let pinEls = new Map() // comment id -> pin element, for the pulse
  let fly = null
  let down = null // {x, y} while the button is held
  let dragging = false
  let lastPointer = null // {x, y, shift} while the cursor is over the canvas
  let hoverDirty = false
  let previewKey = null
  let regionCache = null // {item, mask, pick}: the last face region, reused while the cursor stays in it
  let edgeHi, faceHi, ptHi, hiLineMat, faceHiMat
  let popEl = $state(null), composerEl = $state(null), hintEl = $state(null) // bound elements
  const raycaster = new THREE.Raycaster()
  const ndc = new THREE.Vector2()
  const _v = new THREE.Vector3()
  const _pv = new THREE.Matrix4()
  const r4 = (v) => Math.round(v * 1e4) / 1e4
  const clamp = (v, lo, hi) => Math.max(lo, Math.min(v, hi))

  function themeColors() {
    const css = getComputedStyle(document.documentElement)
    return {
      bg: css.getPropertyValue('--viewer-bg').trim() || '#f4f4f2',
      grid: css.getPropertyValue('--viewer-grid').trim() || '#d6d5cf',
      plate: css.getPropertyValue('--viewer-plate').trim() || '#e9e8e3',
      model: css.getPropertyValue('--model').trim() || '#e8a33d',
      accent: css.getPropertyValue('--accent').trim() || '#c2410c',
    }
  }

  function buildBed() {
    if (bedGroup) {
      scene.remove(bedGroup)
      bedGroup.traverse((o) => o.geometry?.dispose())
    }
    const c = themeColors()
    bedGroup = new THREE.Group()
    const [w, d] = bed
    const plate = new THREE.Mesh(
      new THREE.PlaneGeometry(w, d),
      new THREE.MeshBasicMaterial({ color: c.plate, transparent: true, opacity: 0.6, side: THREE.DoubleSide }),
    )
    plate.position.z = -0.05
    bedGroup.add(plate)
    // 10 mm grid lines, centered on the origin like a typical OpenSCAD model
    const pts = []
    for (let x = -w / 2; x <= w / 2 + 1e-6; x += 10) pts.push(x, -d / 2, 0, x, d / 2, 0)
    for (let y = -d / 2; y <= d / 2 + 1e-6; y += 10) pts.push(-w / 2, y, 0, w / 2, y, 0)
    const g = new THREE.BufferGeometry()
    g.setAttribute('position', new THREE.Float32BufferAttribute(pts, 3))
    bedGroup.add(new THREE.LineSegments(g, new THREE.LineBasicMaterial({ color: c.grid })))
    const axes = new THREE.AxesHelper(20)
    axes.position.z = 0.05
    bedGroup.add(axes)
    scene.add(bedGroup)
    scene.background = new THREE.Color(c.bg)
    retint()
    if (edgeHi) {
      hiLineMat.color.set(c.accent)
      faceHiMat.color.set(c.accent)
      ptHi.material.color.set(c.accent)
    }
  }

  // Part colours follow the theme (--model), so they are repainted with the bed on a theme change.
  function retint() {
    const tints = partTints(themeColors().model, items.length)
    items.forEach((it, i) => it.material.color.set(tints[i]))
  }

  function modelBox() {
    if (items.length) return new THREE.Box3().setFromObject(modelGroup)
    return new THREE.Box3(new THREE.Vector3(-bed[0] / 2, -bed[1] / 2, 0), new THREE.Vector3(bed[0] / 2, bed[1] / 2, 20))
  }

  function fit() {
    const box = modelBox()
    const size3 = box.getSize(new THREE.Vector3())
    const center = box.getCenter(new THREE.Vector3())
    const radius = Math.max(size3.length() / 2, 5)
    const dist = radius / Math.sin((camera.fov * Math.PI) / 360) * 1.1
    const dir = new THREE.Vector3(1, -1.4, 0.9).normalize()
    camera.position.copy(center).addScaledVector(dir, dist)
    camera.near = dist / 1000
    camera.far = dist * 100
    camera.updateProjectionMatrix()
    controls.target.copy(center)
    controls.update()
  }

  export function view(name) {
    if (!camera) return
    const center = modelBox().getCenter(new THREE.Vector3())
    const dist = camera.position.distanceTo(controls.target)
    const dirs = { top: [0, -0.001, 1], front: [0, -1, 0], right: [1, 0, 0], iso: [1, -1.4, 0.9] }
    camera.position.copy(center).addScaledVector(new THREE.Vector3(...dirs[name]).normalize(), dist)
    controls.target.copy(center)
    controls.update()
  }

  // ---- camera flights (comment pins, saved views) ----
  function flyTo(pos, target) {
    fly = { t0: performance.now(), dur: 650, fromP: camera.position.clone(), fromT: controls.target.clone(), toP: pos, toT: target }
  }

  function stepFly(now) {
    const k = Math.min(1, (now - fly.t0) / fly.dur)
    const e = k < 0.5 ? 4 * k * k * k : 1 - Math.pow(-2 * k + 2, 3) / 2
    camera.position.lerpVectors(fly.fromP, fly.toP, e)
    controls.target.lerpVectors(fly.fromT, fly.toT, e)
    if (k >= 1) fly = null
  }

  /** Fly to a comment's anchor (keeping the current viewing direction) and pulse its pin. */
  export function showComment(c) {
    const pt = c?.anchor?.point
    if (!pt || !camera) return
    const target = new THREE.Vector3(...pt)
    const offset = camera.position.clone().sub(controls.target)
    flyTo(target.clone().add(offset), target)
    pulse(c.id)
  }

  /** Restore the camera that was in place when the comment was made. */
  function restoreView(c) {
    const cam = c.anchor?.camera
    if (!cam) return
    flyTo(new THREE.Vector3(...cam.position), new THREE.Vector3(...cam.target))
  }

  function pulse(id) {
    const el = pinEls.get(id)
    if (!el) return
    el.classList.remove('pulse')
    void el.offsetWidth // restart the animation
    el.classList.add('pulse')
    setTimeout(() => el.classList.remove('pulse'), 1800)
  }

  // ---- loading ----
  async function fetchGeom(u) {
    const res = await fetch(u)
    if (res.status === 404) return null
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    return new STLLoader().parse(await res.arrayBuffer())
  }

  async function apply(p) {
    if (p.key === appliedKey) return
    appliedKey = p.key
    const gen = ++applyGen
    loadError = ''
    try {
      let list = null
      if (p.parts) {
        // A failing part falls back to model.stl rather than showing half a model.
        const geoms = await Promise.all(p.parts.map((n) => fetchGeom(api.partStlUrl(fitKey, n)).catch(() => null)))
        if (gen !== applyGen) return
        if (geoms.every(Boolean)) list = p.parts.map((name, i) => ({ name, geom: geoms[i] }))
      }
      if (!list && p.fallback) {
        const g = await fetchGeom(p.fallback)
        if (gen !== applyGen) return
        if (g) list = [{ name: '', geom: g }]
      }
      if (!list) {
        clearItems()
        empty = true
        return
      }
      setItems(list)
      empty = false
      if (fittedFor !== fitKey) {
        fit()
        fittedFor = fitKey
      }
    } catch (e) {
      if (gen === applyGen) loadError = String(e.message || e)
    }
  }

  function setItems(list) {
    clearItems()
    const tints = partTints(themeColors().model, list.length)
    list.forEach((it, i) => {
      const material = new THREE.MeshStandardMaterial({ color: tints[i], roughness: 0.55, metalness: 0.05, flatShading: true })
      const mesh = new THREE.Mesh(it.geom, material)
      const edgeGeom = new THREE.EdgesGeometry(it.geom, 30)
      const edges = new THREE.LineSegments(
        edgeGeom,
        new THREE.LineBasicMaterial({ color: 0x000000, transparent: true, opacity: 0.25 }),
      )
      const item = { name: it.name, geom: it.geom, mesh, edges, material, segs: edgeGeom.attributes.position.array, topo: null }
      mesh.userData.item = item
      modelGroup.add(mesh, edges)
      items.push(item)
    })
    itemsVersion++
  }

  function clearItems() {
    cancelPreview()
    for (const it of items) {
      modelGroup.remove(it.mesh, it.edges)
      it.geom.dispose()
      it.material.dispose()
      it.edges.geometry.dispose()
      it.edges.material.dispose()
    }
    items = []
    regionCache = null
    itemsVersion++
  }

  // Hidden parts, hover dimming and the wireframe toggle all come down to per-item flags.
  $effect(() => {
    itemsVersion
    const hp = hoverPart
    const hid = hidden
    const wf = wireframe
    for (const it of items) {
      const off = hid.has(it.name)
      const dim = hp != null && hp !== it.name
      it.mesh.visible = !off
      it.edges.visible = !off && !wf
      it.material.wireframe = wf
      it.material.transparent = dim
      it.material.opacity = dim ? 0.2 : 1
      it.material.depthWrite = !dim
      it.material.emissive.set(hp != null && hp === it.name ? 0x4a2c08 : 0x000000)
      it.edges.material.opacity = dim ? 0.05 : 0.25
    }
  })

  // ---- overlays: part names, feature pins, comment pins ----
  function clearOverlays() {
    for (const o of overlays) {
      o.removeFromParent()
      o.element.remove()
    }
    overlays = []
    pinEls = new Map()
  }

  function addOverlay(o) {
    scene.add(o)
    overlays.push(o)
  }

  function rebuildOverlays(parts, features, cms, showLabels) {
    clearOverlays()
    for (const p of parts) {
      if (!p.bbox) continue
      const el = document.createElement('div')
      el.className = p.name === '(unlabelled)' ? 'part-label unlabelled' : 'part-label'
      el.textContent = p.name === '(unlabelled)' ? 'unlabelled' : p.name
      const o = new CSS2DObject(el)
      o.center.set(0.5, 1) // bottom edge of the label sits on the point, so it floats above the part
      o.position.set(...labelPoint(p.bbox))
      o.visible = showLabels
      addOverlay(o)
    }
    features.forEach((f, i) => {
      const el = document.createElement('button')
      el.type = 'button'
      el.className = 'feat'
      el.textContent = String(i + 1)
      el.setAttribute('aria-label', f.name)
      const tip = document.createElement('span')
      tip.className = 'tip'
      tip.textContent = f.file ? `${f.name} · ${f.file}:${f.line}` : f.name
      el.append(tip)
      el.addEventListener('click', (e) => {
        e.stopPropagation()
        if (f.file) onjump(f)
      })
      const o = new CSS2DObject(el)
      o.position.set(...f.at)
      o.visible = showLabels
      addOverlay(o)
    })
    for (const c of cms) {
      const a = c.anchor
      if (!a?.point || c.resolved) continue
      const el = document.createElement('button')
      el.type = 'button'
      el.className = a.stale ? 'pin stale' : 'pin'
      el.textContent = String(c.id)
      el.title = `Comment #${c.id}${a.stale ? ' (the model changed since)' : ''}`
      el.addEventListener('click', (e) => {
        e.stopPropagation()
        openId = c.id
      })
      pinEls.set(c.id, el)
      const o = new CSS2DObject(el)
      o.position.set(...a.point)
      addOverlay(o)
    }
  }

  $effect(() => {
    if (!ready) return
    rebuildOverlays(labelParts, shownFeatures, comments, labelsOn)
  })

  // ---- the parts payload and the scene plan ----
  async function loadPartsInfo(name) {
    const gen = ++partsGen
    const info = await api.parts(name).catch(() => null) // 404 or error: no parts
    if (gen !== partsGen || name !== fitKey) return
    partsInfo = info && Array.isArray(info.parts) ? info : null
  }

  // A different project: drop everything that belonged to the old one.
  function resetScene() {
    applyGen++
    partsGen++
    appliedKey = null
    clearItems()
    clearOverlays()
    partsInfo = null
    hidden = new Set()
    hover3d = hoverList = null
    openId = null
    composer = null
  }

  $effect(() => {
    const name = fitKey
    if (!ready) return
    untrack(() => {
      if (name === projectName) return
      projectName = name
      resetScene()
    })
  })

  $effect(() => {
    const name = fitKey
    renderVersion
    partsVersion
    if (ready && name) loadPartsInfo(name)
  })

  $effect(() => {
    const p = plan
    if (ready) untrack(() => apply(p))
  })

  $effect(() => {
    bed // rebuild when the bed size arrives from /api/config
    if (ready) buildBed()
  })

  // ---- picking ----
  function raycastAt(x, y) {
    const vis = items.filter((it) => it.mesh.visible)
    if (!vis.length) return { vis, hit: null }
    ndc.set((x / size.w) * 2 - 1, -(y / size.h) * 2 + 1)
    raycaster.setFromCamera(ndc, camera)
    const hits = raycaster.intersectObjects(vis.map((it) => it.mesh), false)
    return { vis, hit: hits[0] ?? null }
  }

  function pvElements() {
    _pv.multiplyMatrices(camera.projectionMatrix, camera.matrixWorldInverse)
    return _pv.elements
  }

  /**
   * What a click at (x, y) would comment on. Shift forces a point on the surface. Otherwise an edge
   * within EDGE_PICK_PX wins; failing that the face under the cursor, grown to its coplanar region.
   * Edges behind the surface are skipped: they must be no farther than the surface hit.
   */
  function pickAt(x, y, forcePoint) {
    const { vis, hit } = raycastAt(x, y)
    if (forcePoint) {
      if (hit) return { kind: 'point', item: hit.object.userData.item, point: hit.point.toArray(), key: pointKey(hit.object.userData.item, hit.point) }
      const p = new THREE.Vector3()
      if (!raycaster.ray.intersectPlane(new THREE.Plane(new THREE.Vector3(0, 0, 1), 0), p)) return null
      return { kind: 'point', item: null, point: p.toArray(), key: pointKey(null, p) }
    }

    const pv = pvElements()
    const cam = camera.position
    const faceDist = hit ? hit.distance : null
    const tol = Math.max(0.5, 0.01 * (faceDist ?? 0))
    let best = null
    for (const it of vis) {
      const segs = it.segs
      const r = nearestEdge(
        segs, pv, size.w, size.h, x, y, EDGE_PICK_PX,
        faceDist == null ? null : (s, t) => {
          const o = 6 * s
          const px = segs[o] + (segs[o + 3] - segs[o]) * t
          const py = segs[o + 1] + (segs[o + 4] - segs[o + 1]) * t
          const pz = segs[o + 2] + (segs[o + 5] - segs[o + 2]) * t
          return Math.hypot(px - cam.x, py - cam.y, pz - cam.z) <= faceDist + tol
        },
      )
      if (r && (!best || r.dist < best.r.dist)) best = { it, r }
    }
    if (best) {
      const { it, r } = best
      const o = 6 * r.s
      const sg = it.segs
      const a = [sg[o], sg[o + 1], sg[o + 2]]
      const b = [sg[o + 3], sg[o + 4], sg[o + 5]]
      return {
        kind: 'edge', item: it, a, b, point: a.map((v, i) => v + (b[i] - v) * r.t), key: `e|${it.name}|${r.s}`,
      }
    }

    if (hit) {
      const it = hit.object.userData.item
      const seed = hit.faceIndex
      if (!it.topo) it.topo = buildTopology(it.geom.attributes.position.array)
      const n = it.topo.normals
      const normal = [n[3 * seed], n[3 * seed + 1], n[3 * seed + 2]]
      if (regionCache?.item === it && regionCache.mask[seed]) {
        // Still inside the last region: same preview, only the point moves.
        return { ...regionCache.pick, point: hit.point.toArray() }
      }
      const tris = growRegion(it.topo, seed)
      const mask = new Uint8Array(it.topo.T)
      for (const t of tris) mask[t] = 1
      const pick = { kind: 'face', item: it, tris, normal, key: `f|${it.name}|${seed}` }
      regionCache = { item: it, mask, pick }
      return { ...pick, point: hit.point.toArray() }
    }
    return null
  }

  const pointKey = (item, p) => `p|${item?.name ?? ''}|${Math.round(p.x * 20)}|${Math.round(p.y * 20)}|${Math.round(p.z * 20)}`

  function describe(p) {
    const who = p.item?.name || 'model'
    return `${who} · ${p.kind}${p.kind === 'face' ? ` (${p.tris.length} tris)` : ''}`
  }

  function setPreview(p) {
    const key = p?.key ?? null
    if (key === previewKey) return
    previewKey = key
    edgeHi.visible = faceHi.visible = ptHi.visible = false
    if (!p) {
      hintText = ''
      return
    }
    if (p.kind === 'edge') {
      edgeHi.geometry.setPositions([...p.a, ...p.b])
      edgeHi.visible = true
    } else if (p.kind === 'face') {
      faceHi.geometry.dispose()
      const g = new THREE.BufferGeometry()
      g.setAttribute('position', new THREE.BufferAttribute(regionPositions(p.item.geom.attributes.position.array, p.tris), 3))
      faceHi.geometry = g
      faceHi.visible = true
    } else {
      ptHi.position.set(...p.point)
      ptHi.visible = true
    }
    hintText = describe(p)
  }

  function cancelPreview() {
    previewKey = null
    if (edgeHi) edgeHi.visible = faceHi.visible = ptHi.visible = false
    hintText = ''
  }

  function runHover() {
    if (!lastPointer) {
      setPreview(null)
      hover3d = null
      return
    }
    if (dragging) return
    const { x, y, shift } = lastPointer
    if (commentMode) {
      hover3d = null
      setPreview(pickAt(x, y, shift))
    } else {
      setPreview(null)
      // Only labelled parts can be hovered; skip the raycast over a plain model.
      hover3d = items.some((it) => it.name) ? raycastAt(x, y).hit?.object.userData.item.name || null : null
    }
    if (hintEl) hintEl.style.transform = `translate(${x + 14}px, ${y + 14}px)`
  }

  function setCommentMode(on_) {
    commentMode = on_
    composer = null
    if (!on_) {
      lastPointer = null
      hoverDirty = true
    }
  }

  function viewNow() {
    return { position: camera.position.toArray(), target: controls.target.toArray() }
  }

  function onClick(x, y, shift) {
    if (!commentMode) {
      openId = null
      return
    }
    const p = pickAt(x, y, shift)
    if (!p) return
    // Keep the draft if the user moves the anchor while typing.
    composer = { x, y, pick: p, view: viewNow() }
  }

  async function saveComment() {
    const c = composer
    if (!c || !composerText.trim() || saving) return
    const p = c.pick
    const anchor = {
      part: p.item?.name ?? '',
      kind: p.kind,
      point: p.point.map(r4),
      camera: { position: c.view.position.map(r4), target: c.view.target.map(r4) },
      stale: false,
    }
    if (p.kind === 'face') anchor.normal = p.normal.map(r4)
    if (p.kind === 'edge') anchor.edge = [p.a.map(r4), p.b.map(r4)]
    saving = true
    try {
      const ok = await on.add?.({ body: composerText, anchor })
      if (ok) {
        composer = null
        composerText = ''
      }
    } finally {
      saving = false
    }
  }

  function composerKey(e) {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') saveComment()
    if (e.key === 'Escape') composer = null
  }

  function toggleHidden(name) {
    const s = new Set(hidden)
    if (s.has(name)) s.delete(name)
    else s.add(name)
    hidden = s
  }

  // Floating popovers follow their anchor every frame; the DOM is written directly, not via state.
  function placeFloating() {
    if (popEl && openComment?.anchor?.point) {
      _v.set(...openComment.anchor.point).project(camera)
      const x = (_v.x + 1) / 2 * size.w
      const y = (1 - _v.y) / 2 * size.h
      const w = popEl.offsetWidth, h = popEl.offsetHeight
      popEl.style.visibility = _v.z > 1 ? 'hidden' : 'visible'
      popEl.style.transform = `translate(${clamp(x + 16, 8, size.w - w - 8)}px, ${clamp(y - h / 2, 8, size.h - h - 8)}px)`
    }
    if (composerEl && composer) {
      const w = composerEl.offsetWidth, h = composerEl.offsetHeight
      composerEl.style.transform = `translate(${clamp(composer.x + 14, 8, size.w - w - 8)}px, ${clamp(composer.y + 14, 8, size.h - h - 8)}px)`
    }
  }

  onMount(() => {
    renderer = new THREE.WebGLRenderer({ antialias: true })
    renderer.setPixelRatio(window.devicePixelRatio)
    host.appendChild(renderer.domElement)
    labelRenderer = new CSS2DRenderer()
    labelRenderer.domElement.className = 'labels-layer'
    host.appendChild(labelRenderer.domElement)

    scene = new THREE.Scene()
    camera = new THREE.PerspectiveCamera(40, 1, 0.1, 10000)
    controls = new OrbitControls(camera, renderer.domElement)
    controls.enableDamping = true
    controls.addEventListener('start', () => (fly = null)) // the user takes over from a flight
    modelGroup = new THREE.Group()
    scene.add(modelGroup)
    scene.add(new THREE.HemisphereLight(0xffffff, 0x8d8d8d, 1.6))
    const sun = new THREE.DirectionalLight(0xffffff, 1.8)
    sun.position.set(1, -2, 3)
    camera.add(sun) // light follows the camera, so no side is ever black
    scene.add(camera)

    // Highlights: a thick edge, a translucent face region and a point marker.
    const accent = themeColors().accent
    hiLineMat = new LineMaterial({ linewidth: 4 })
    hiLineMat.color.set(accent)
    edgeHi = new LineSegments2(new LineSegmentsGeometry(), hiLineMat)
    edgeHi.geometry.setPositions([0, 0, 0, 0, 0, 0])
    faceHiMat = new THREE.MeshBasicMaterial({
      color: accent, transparent: true, opacity: 0.4, side: THREE.DoubleSide, depthWrite: false,
      polygonOffset: true, polygonOffsetFactor: -2, polygonOffsetUnits: -2,
    })
    faceHi = new THREE.Mesh(new THREE.BufferGeometry(), faceHiMat)
    ptHi = new THREE.Mesh(new THREE.SphereGeometry(0.8, 12, 8), new THREE.MeshBasicMaterial({ color: accent, depthTest: false }))
    for (const o of [edgeHi, faceHi, ptHi]) {
      o.visible = false
      o.frustumCulled = false
      o.renderOrder = 5
      scene.add(o)
    }

    buildBed()
    fit()

    const local = (e) => {
      const r = renderer.domElement.getBoundingClientRect()
      return { x: e.clientX - r.left, y: e.clientY - r.top }
    }
    const canvas = renderer.domElement
    const onDown = (e) => {
      if (e.button !== 0) return
      fly = null
      down = local(e)
      dragging = false
    }
    const onMove = (e) => {
      const p = local(e)
      if (down && Math.hypot(p.x - down.x, p.y - down.y) > 5) dragging = true
      if (dragging) {
        if (previewKey) cancelPreview()
        return
      }
      lastPointer = { x: p.x, y: p.y, shift: e.shiftKey }
      hoverDirty = true
    }
    const onUp = (e) => {
      if (!down) return
      const p = local(e)
      const click = !dragging && Math.hypot(p.x - down.x, p.y - down.y) <= 5
      down = null
      dragging = false
      if (click) onClick(p.x, p.y, e.shiftKey)
    }
    const onLeave = () => {
      if (!dragging) {
        lastPointer = null
        hoverDirty = true
      }
    }
    canvas.addEventListener('pointerdown', onDown)
    canvas.addEventListener('pointermove', onMove)
    canvas.addEventListener('pointerup', onUp)
    canvas.addEventListener('pointerleave', onLeave)

    const onKey = (e) => {
      if (e.target instanceof HTMLElement && e.target.closest('input, textarea, select, [contenteditable="true"]')) return
      if (e.ctrlKey || e.metaKey || e.altKey) return
      if (e.key === 'c' || e.key === 'C') setCommentMode(!commentMode)
      else if (e.key === 'Escape') {
        if (composer) composer = null
        else if (openId != null) openId = null
        else if (commentMode) setCommentMode(false)
      }
    }
    window.addEventListener('keydown', onKey)

    const resize = () => {
      const { clientWidth: w, clientHeight: h } = host
      if (!w || !h) return
      size = { w, h }
      renderer.setSize(w, h)
      labelRenderer.setSize(w, h)
      camera.aspect = w / h
      camera.updateProjectionMatrix()
      hiLineMat.resolution.set(w, h)
    }
    const ro = new ResizeObserver(resize)
    ro.observe(host)
    resize()

    const media = matchMedia('(prefers-color-scheme: dark)')
    const onTheme = () => buildBed()
    media.addEventListener('change', onTheme)

    const loop = () => {
      frame = requestAnimationFrame(loop)
      if (fly) stepFly(performance.now())
      controls.update()
      if (hoverDirty) {
        hoverDirty = false
        runHover()
      }
      renderer.render(scene, camera)
      labelRenderer.render(scene, camera)
      placeFloating()
    }
    loop()
    ready = true

    return () => {
      cancelAnimationFrame(frame)
      ro.disconnect()
      media.removeEventListener('change', onTheme)
      window.removeEventListener('keydown', onKey)
      canvas.removeEventListener('pointerdown', onDown)
      canvas.removeEventListener('pointermove', onMove)
      canvas.removeEventListener('pointerup', onUp)
      canvas.removeEventListener('pointerleave', onLeave)
      clearOverlays()
      clearItems()
      for (const o of [edgeHi, faceHi, ptHi]) o.geometry.dispose()
      faceHiMat.dispose()
      hiLineMat.dispose()
      ptHi.material.dispose()
      bedGroup?.traverse((o) => o.geometry?.dispose())
      controls.dispose()
      renderer.dispose()
    }
  })
</script>

<div class="viewer">
  <div class="canvas" class:comment={commentMode} bind:this={host}></div>

  <div class="tools" class:with-parts={listParts.length || shownFeatures.length}>
    <button onclick={() => view('iso')} title="Isometric">Iso</button>
    <button onclick={() => view('front')}>Front</button>
    <button onclick={() => view('right')}>Side</button>
    <button onclick={() => view('top')}>Top</button>
    <button onclick={fit} title="Fit model in view">Fit</button>
    <button class="toggle" class:on={labelsOn} aria-pressed={labelsOn} onclick={() => (labelsOn = !labelsOn)}
      title="Part names and feature numbers">Labels</button>
    <button class="toggle" class:on={commentMode} aria-pressed={commentMode} onclick={() => setCommentMode(!commentMode)}
      title="Comment on the model (C, Esc to leave)">Comment</button>
    <label><input type="checkbox" bind:checked={wireframe}> Wireframe</label>
  </div>

  {#if listParts.length || shownFeatures.length}
    <PartsList
      parts={listParts}
      features={shownFeatures}
      live={usable}
      {hidden}
      hovered={hoverPart}
      onhover={(n) => (hoverList = n)}
      ontoggle={toggleHidden}
      {onjump}
    />
  {/if}

  <div class="bottom-bar">
    {#if pending}<span class="chip">labelling parts…</span>{/if}
    {#if commentMode}<span class="chip">Comment: hover to preview, click to comment, Shift-click for a point, Esc to leave</span>{/if}
  </div>

  {#if hintText && !composer}
    <div class="hint-chip" bind:this={hintEl}>{hintText}</div>
  {/if}

  {#if composer}
    <div class="pop composer" bind:this={composerEl} role="dialog" aria-label="New comment">
      <div class="pop-head muted">Comment on {describe(composer.pick)}</div>
      <!-- svelte-ignore a11y_autofocus -->
      <textarea bind:value={composerText} onkeydown={composerKey} rows="3" autofocus
        placeholder="What should change here? (Ctrl+Enter to save)"></textarea>
      <div class="actions">
        <button class="primary" disabled={saving || !composerText.trim()} onclick={saveComment}>{saving ? 'Saving…' : 'Save'}</button>
        <button onclick={() => (composer = null)}>Cancel</button>
      </div>
    </div>
  {/if}

  {#if openComment}
    <div class="pop thread-pop" bind:this={popEl} role="dialog" aria-label="Comment on the model">
      <div class="pop-head">
        <span class="muted">{openComment.anchor.part ? `On ${openComment.anchor.part}` : 'On the model'}</span>
        <button class="close" onclick={() => (openId = null)} aria-label="Close">×</button>
      </div>
      {#if openComment.anchor.stale}
        <div class="stale-note">The model changed after this comment was made, so the pin may no longer sit on the same spot.</div>
      {/if}
      <CommentThread comment={openComment} {on} onshow={restoreView} showLabel="Show view" />
    </div>
  {/if}

  {#if empty}
    <div class="overlay">No rendered model yet</div>
  {/if}
  {#if loadError}
    <div class="overlay error">Couldn't load model: {loadError}</div>
  {/if}
</div>

<style>
  .viewer { position: relative; width: 100%; height: 100%; min-height: 0; overflow: hidden; }
  .canvas { position: absolute; inset: 0; }
  .canvas :global(canvas) { display: block; }
  .canvas.comment :global(canvas) { cursor: crosshair; }
  :global(.labels-layer) { position: absolute; inset: 0; pointer-events: none; overflow: hidden; }
  :global(.labels-layer > *) { pointer-events: none; }
  :global(.labels-layer .pin), :global(.labels-layer .feat) { pointer-events: auto; }

  .tools {
    position: absolute; top: 8px; left: 8px; display: flex; flex-wrap: wrap; gap: 4px; align-items: center;
    max-width: calc(100% - 16px); z-index: 3;
  }
  .tools.with-parts { max-width: calc(100% - 130px); }
  .tools button, .tools label {
    font-size: 12px; padding: 3px 8px; background: var(--card); color: var(--text);
    border: 1px solid var(--line); border-radius: 6px; cursor: pointer;
  }
  .tools button.toggle.on { background: var(--accent); border-color: var(--accent); color: #fff; }
  .tools label { display: flex; gap: 4px; align-items: center; }

  .bottom-bar {
    position: absolute; left: 8px; right: 8px; bottom: 8px; display: flex; flex-direction: column; gap: 4px;
    align-items: flex-start; pointer-events: none; z-index: 3;
  }
  .chip {
    font-size: 12px; background: var(--card); color: var(--muted); border: 1px solid var(--line);
    border-radius: 6px; padding: 2px 8px; max-width: 100%;
  }

  .hint-chip {
    position: absolute; left: 0; top: 0; z-index: 4; pointer-events: none; white-space: nowrap;
    font: 12px var(--mono); background: var(--card); color: var(--text); border: 1px solid var(--line);
    border-radius: 6px; padding: 2px 7px;
  }

  .pop {
    position: absolute; left: 0; top: 0; z-index: 5; width: min(320px, calc(100% - 16px));
    background: var(--card); border: 1px solid var(--line); border-radius: 8px;
    box-shadow: 0 6px 20px rgb(0 0 0 / 0.18); padding: 8px; display: flex; flex-direction: column; gap: 6px;
    font: 13px system-ui, sans-serif; max-height: calc(100% - 16px); overflow: auto;
  }
  .pop-head { display: flex; align-items: center; gap: 6px; font-size: 12px; }
  .pop-head .close { margin-left: auto; border: 0; background: none; padding: 0 6px; font-size: 16px; line-height: 1; }
  .pop textarea {
    width: 100%; font: 13px system-ui, sans-serif; background: var(--card); color: var(--text);
    border: 1px solid var(--line); border-radius: 6px; padding: 6px; resize: vertical;
  }
  .pop .actions { display: flex; gap: 6px; }
  .thread-pop :global(.thread) { margin: 0; }
  .stale-note {
    font-size: 12px; color: var(--warn); background: var(--warn-soft); border-radius: 6px; padding: 4px 6px;
  }
  .muted { color: var(--muted); }

  .overlay {
    position: absolute; inset: 0; display: grid; place-items: center; pointer-events: none;
    color: var(--muted); font-size: 14px;
  }
  .overlay.error { color: var(--err); }

  /* DOM made in code (CSS2D), so these are global */
  :global(.part-label) {
    white-space: nowrap; font: 600 12px/1.2 system-ui, sans-serif; color: var(--text);
    background: color-mix(in srgb, var(--card) 88%, transparent); border: 1px solid var(--line);
    border-radius: 5px; padding: 2px 6px; box-shadow: 0 1px 3px rgb(0 0 0 / 0.15);
  }
  :global(.part-label.unlabelled) { color: var(--muted); font-style: italic; font-weight: 500; }
  :global(.pin) {
    width: 20px; height: 20px; border-radius: 50%; border: 2px solid var(--card); background: var(--accent);
    color: #fff; font: 600 11px/16px system-ui, sans-serif; text-align: center; padding: 0; cursor: pointer;
    box-shadow: 0 1px 4px rgb(0 0 0 / 0.35);
  }
  :global(.pin.stale) { opacity: 0.45; border-style: dashed; background: var(--muted); }
  :global(.pin.pulse) { animation: -global-openscad-pin-pulse 0.8s ease-out 2; }
  @keyframes -global-openscad-pin-pulse {
    from { box-shadow: 0 0 0 0 var(--accent); }
    to { box-shadow: 0 0 0 16px transparent; }
  }
  :global(.feat) {
    position: relative; width: 16px; height: 16px; border-radius: 50%; padding: 0;
    border: 1.5px solid var(--accent); background: var(--card); color: var(--accent);
    font: 600 10px/13px system-ui, sans-serif; cursor: default;
  }
  :global(.feat .tip) {
    display: none; position: absolute; left: 50%; bottom: calc(100% + 6px); transform: translateX(-50%);
    white-space: nowrap; background: var(--card); color: var(--text); border: 1px solid var(--line);
    border-radius: 6px; padding: 2px 6px; font: 12px system-ui, sans-serif; pointer-events: none;
  }
  :global(.feat:hover .tip), :global(.feat:focus-visible .tip) { display: block; }
</style>
