// Pure helpers for the parts view and comment picking: no DOM, no three.js, so they can be
// checked in node (see the harness in the scratchpad). Geometry is passed as plain arrays:
// `pos` is a non-indexed position array (9 floats per triangle, as STLLoader produces).

export const NORMAL_TOL_DEG = 1 // a face region is coplanar within this angle of its seed
export const EDGE_PICK_PX = 6 // an edge is picked when the cursor is this close (screen px)

/** model_time and stl_time are RFC3339 strings from the same server; compare them as instants. */
export function sameTime(a, b) {
  if (!a || !b) return false
  return a === b || Date.parse(a) === Date.parse(b)
}

/** Centre of a bbox at its top: where a part's name label sits. */
export function labelPoint(bbox) {
  const { min, max } = bbox
  return [(min[0] + max[0]) / 2, (min[1] + max[1]) / 2, max[2]]
}

/**
 * Triangle topology with vertices welded by position (STL repeats every corner per triangle,
 * so this is what makes "shared vertices" mean something). Built lazily, once per mesh.
 */
export function buildTopology(pos) {
  const T = pos.length / 9
  const ids = new Map()
  const triVerts = new Uint32Array(3 * T)
  for (let t = 0; t < T; t++) {
    for (let k = 0; k < 3; k++) {
      const o = t * 9 + k * 3
      const key = `${Math.round(pos[o] * 1e4)},${Math.round(pos[o + 1] * 1e4)},${Math.round(pos[o + 2] * 1e4)}`
      let id = ids.get(key)
      if (id === undefined) {
        id = ids.size
        ids.set(key, id)
      }
      triVerts[3 * t + k] = id
    }
  }
  const V = ids.size
  // CSR: vertex -> triangles touching it
  const start = new Uint32Array(V + 1)
  for (let i = 0; i < 3 * T; i++) start[triVerts[i] + 1]++
  for (let v = 0; v < V; v++) start[v + 1] += start[v]
  const fill = start.slice(0, V)
  const list = new Uint32Array(3 * T)
  for (let i = 0; i < 3 * T; i++) list[fill[triVerts[i]]++] = Math.floor(i / 3)
  // unit normals per triangle; degenerate triangles keep (0,0,0) and never join a region
  const normals = new Float32Array(3 * T)
  for (let t = 0; t < T; t++) {
    const o = t * 9
    const ux = pos[o + 3] - pos[o], uy = pos[o + 4] - pos[o + 1], uz = pos[o + 5] - pos[o + 2]
    const vx = pos[o + 6] - pos[o], vy = pos[o + 7] - pos[o + 1], vz = pos[o + 8] - pos[o + 2]
    let nx = uy * vz - uz * vy, ny = uz * vx - ux * vz, nz = ux * vy - uy * vx
    const len = Math.hypot(nx, ny, nz)
    if (len > 0) {
      nx /= len
      ny /= len
      nz /= len
    }
    normals[3 * t] = nx
    normals[3 * t + 1] = ny
    normals[3 * t + 2] = nz
  }
  return { T, triVerts, start, list, normals }
}

/**
 * The face region around a triangle: every triangle reachable through shared vertices whose
 * normal is within `tolDeg` of the seed's normal. The seed's normal is the reference (not the
 * neighbour's), so a gently curved surface does not drift into a region.
 */
export function growRegion(topo, seed, tolDeg = NORMAL_TOL_DEG) {
  const { T, triVerts, start, list, normals } = topo
  const nx = normals[3 * seed], ny = normals[3 * seed + 1], nz = normals[3 * seed + 2]
  if (nx === 0 && ny === 0 && nz === 0) return Uint32Array.of(seed)
  const cos = Math.cos((tolDeg * Math.PI) / 180)
  const seen = new Uint8Array(T)
  const out = [seed]
  seen[seed] = 1
  for (let i = 0; i < out.length; i++) {
    const t = out[i]
    for (let k = 0; k < 3; k++) {
      const v = triVerts[3 * t + k]
      for (let j = start[v]; j < start[v + 1]; j++) {
        const u = list[j]
        if (seen[u]) continue
        seen[u] = 1
        const d = normals[3 * u] * nx + normals[3 * u + 1] * ny + normals[3 * u + 2] * nz
        if (d >= cos) out.push(u)
      }
    }
  }
  return Uint32Array.from(out)
}

/** Positions (9 floats per triangle) of the given triangles, for an overlay mesh. */
export function regionPositions(pos, tris) {
  const out = new Float32Array(tris.length * 9)
  for (let i = 0; i < tris.length; i++) out.set(pos.subarray(tris[i] * 9, tris[i] * 9 + 9), i * 9)
  return out
}

// Scratch for projecting: column-major clip matrix pv (projection x view), as Matrix4.elements.
const clip = new Float64Array(3)
function toScreen(pv, x, y, z, w, h) {
  const cw = pv[3] * x + pv[7] * y + pv[11] * z + pv[15]
  if (cw <= 1e-9) return false
  const nx = (pv[0] * x + pv[4] * y + pv[8] * z + pv[12]) / cw
  const ny = (pv[1] * x + pv[5] * y + pv[9] * z + pv[13]) / cw
  clip[0] = (nx + 1) * 0.5 * w
  clip[1] = (1 - ny) * 0.5 * h
  clip[2] = cw
  return true
}

/**
 * Nearest edge segment to the cursor (px, py) in screen space, within maxPx.
 * segs: 6 floats per segment (a.xyz, b.xyz), as EdgesGeometry gives them.
 * accept(s, t): optional veto for a candidate (used to skip edges behind the surface);
 * candidates are tried nearest first, so a vetoed edge never hides a visible one.
 * Returns {s, t, dist} with t the position along the segment of the closest screen point.
 */
export function nearestEdge(segs, pv, w, h, px, py, maxPx = EDGE_PICK_PX, accept = null) {
  let best = null
  const S = segs.length / 6
  for (let s = 0; s < S; s++) {
    const o = 6 * s
    if (!toScreen(pv, segs[o], segs[o + 1], segs[o + 2], w, h)) continue
    const ax = clip[0], ay = clip[1]
    if (!toScreen(pv, segs[o + 3], segs[o + 4], segs[o + 5], w, h)) continue
    const bx = clip[0], by = clip[1]
    const dx = bx - ax, dy = by - ay
    const len2 = dx * dx + dy * dy
    let t = len2 > 0 ? ((px - ax) * dx + (py - ay) * dy) / len2 : 0
    t = t < 0 ? 0 : t > 1 ? 1 : t
    const d = Math.hypot(ax + dx * t - px, ay + dy * t - py)
    if (d > maxPx) continue
    if (best && d >= best.dist) continue
    if (accept && !accept(s, t)) continue
    best = { s, t, dist: d }
  }
  return best
}

const hex2 = (v) => Math.round(Math.min(255, Math.max(0, v * 255))).toString(16).padStart(2, '0')

function rgbToHsl(r, g, b) {
  const max = Math.max(r, g, b), min = Math.min(r, g, b)
  const l = (max + min) / 2
  if (max === min) return [0, 0, l]
  const d = max - min
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min)
  let h
  if (max === r) h = (g - b) / d + (g < b ? 6 : 0)
  else if (max === g) h = (b - r) / d + 2
  else h = (r - g) / d + 4
  return [h / 6, s, l]
}

function hslToRgb(h, s, l) {
  if (s === 0) return [l, l, l]
  const f = (p, q, t) => {
    if (t < 0) t += 1
    if (t > 1) t -= 1
    if (t < 1 / 6) return p + (q - p) * 6 * t
    if (t < 1 / 2) return q
    if (t < 2 / 3) return p + (q - p) * (2 / 3 - t) * 6
    return p
  }
  const q = l < 0.5 ? l * (1 + s) : l + s - l * s
  const p = 2 * l - q
  return [f(p, q, h + 1 / 3), f(p, q, h), f(p, q, h - 1 / 3)]
}

const LIGHTNESS = [0, 0.08, -0.08, 0.04, -0.04, 0.12, -0.12]
const HUE = [0, 6, -6, 12, -12, 3, -3]

/**
 * n distinct tints of the base colour (#rrggbb) so the parts read as separate pieces but still
 * as one object: same hue family and saturation, lightness and a few degrees of hue varied.
 */
export function partTints(base, n) {
  const m = /^#([0-9a-f]{6})$/i.exec(base || '')
  if (!m || n <= 1) return Array.from({ length: n }, () => base)
  const v = parseInt(m[1], 16)
  const [h, s, l] = rgbToHsl(((v >> 16) & 255) / 255, ((v >> 8) & 255) / 255, (v & 255) / 255)
  const out = []
  for (let i = 0; i < n; i++) {
    const hh = (h * 360 + HUE[i % HUE.length] + 360) % 360 / 360
    const ll = Math.min(0.85, Math.max(0.25, l + LIGHTNESS[i % LIGHTNESS.length]))
    const [r, g, b] = hslToRgb(hh, s, ll)
    out.push(`#${hex2(r)}${hex2(g)}${hex2(b)}`)
  }
  return out
}
