// Toolpath preview: reads OrcaSlicer G-code into extrusion line segments, grouped by feature (";TYPE:Brim",
// ";TYPE:Support", ...) and by layer (";LAYER_CHANGE"). Travel moves and wipes (E <= 0) are left out.
// Handles G90/G91, M82/M83, G92 and arcs (G2/G3 with I/J, from Orca's arc fitting).

// Colours close to OrcaSlicer's own preview.
export const FEATURE_COLORS = {
  'Outer wall': '#ff7d38',
  'Inner wall': '#ffe17d',
  'Overhang wall': '#3b5bdb',
  'Sparse infill': '#b03029',
  'Internal solid infill': '#9654cc',
  'Top surface': '#f05c5c',
  'Bottom surface': '#6b9be0',
  Bridge: '#4d80ba',
  'Internal Bridge': '#4d80ba',
  'Gap infill': '#d9d9d9',
  Skirt: '#00876e',
  Brim: '#00876e',
  Support: '#2fb35a',
  'Support interface': '#1a6b30',
  'Support transition': '#4f9a6a',
  'Prime tower': '#a0a0a0',
  Ironing: '#ffa0d0',
  Custom: '#5e5e5e',
}
export const featureColor = (name) => FEATURE_COLORS[name] ?? '#888888'

const ARC_STEP = 0.5 // mm of arc per line segment

/**
 * Returns {layers: [{z}], features: [{name, positions: Float32Array, layerEnds: Int32Array}]}.
 * positions holds x,y,z of two vertices per segment; layerEnds[i] is the vertex count up to and
 * including layer i, so setDrawRange(0, layerEnds[i]) shows layers 0..i.
 */
export function parseGcode(text) {
  const feats = new Map() // name -> {pos: number[], ends: number[]}
  const layers = []
  let layer = -1
  let type = 'Custom'
  let absXY = true
  let absE = false
  let x = 0, y = 0, z = 0, e = 0

  const feature = (name) => {
    let f = feats.get(name)
    if (!f) feats.set(name, (f = { pos: [], ends: [] }))
    return f
  }
  const seg = (x0, y0, x1, y1, zz) => {
    const L = Math.max(layer, 0)
    const f = feature(type)
    while (f.ends.length < L) f.ends.push(f.pos.length / 3)
    f.pos.push(x0, y0, zz, x1, y1, zz)
  }

  let start = 0
  const n = text.length
  while (start < n) {
    let end = text.indexOf('\n', start)
    if (end < 0) end = n
    const line = text.slice(start, end).trim()
    start = end + 1
    if (!line) continue

    if (line[0] === ';') {
      if (line.startsWith(';TYPE:')) type = line.slice(6).trim()
      else if (line.startsWith(';LAYER_CHANGE')) {
        layer++
        layers.push({ z })
      } else if (line.startsWith(';Z:') && layer >= 0) layers[layer].z = parseFloat(line.slice(3))
      continue
    }

    const code = line.split(';', 1)[0].trim()
    const words = code.split(/\s+/)
    const cmd = words[0].toUpperCase()
    if (cmd === 'G90') { absXY = true; continue }
    if (cmd === 'G91') { absXY = false; continue }
    if (cmd === 'M82') { absE = true; continue }
    if (cmd === 'M83') { absE = false; continue }
    if (cmd === 'G92') {
      for (const w of words.slice(1)) if (w[0] === 'E' || w[0] === 'e') e = parseFloat(w.slice(1)) || 0
      continue
    }
    if (cmd !== 'G0' && cmd !== 'G1' && cmd !== 'G2' && cmd !== 'G3') continue

    let nx = x, ny = y, nz = z, de = 0, hasE = false, I = 0, J = 0
    for (let k = 1; k < words.length; k++) {
      const w = words[k]
      const v = parseFloat(w.slice(1))
      if (Number.isNaN(v)) continue
      switch (w[0]) {
        case 'X': case 'x': nx = absXY ? v : x + v; break
        case 'Y': case 'y': ny = absXY ? v : y + v; break
        case 'Z': case 'z': nz = absXY ? v : z + v; break
        case 'E': case 'e':
          hasE = true
          de = absE ? v - e : v
          e = absE ? v : e + v
          break
        case 'I': case 'i': I = v; break
        case 'J': case 'j': J = v; break
      }
    }
    const extruding = hasE && de > 0 && cmd !== 'G0'
    if (extruding && (cmd === 'G2' || cmd === 'G3')) arc(x, y, nx, ny, I, J, cmd === 'G2', nz, seg)
    else if (extruding && (nx !== x || ny !== y)) seg(x, y, nx, ny, nz)
    x = nx
    y = ny
    z = nz
  }

  const total = Math.max(layers.length, 1)
  const features = []
  for (const [name, f] of feats) {
    while (f.ends.length < total) f.ends.push(f.pos.length / 3)
    features.push({ name, positions: new Float32Array(f.pos), layerEnds: Int32Array.from(f.ends) })
  }
  if (!layers.length) layers.push({ z: 0 })
  return { layers, features }
}

// Splits an arc from (x0,y0) to (x1,y1) around (x0+I, y0+J) into short segments. Equal end points = full circle.
function arc(x0, y0, x1, y1, I, J, cw, z, seg) {
  const cx = x0 + I, cy = y0 + J
  const r = Math.hypot(I, J)
  const a0 = Math.atan2(y0 - cy, x0 - cx)
  let a1 = Math.atan2(y1 - cy, x1 - cx)
  let sweep = a1 - a0
  if (cw && sweep >= 0) sweep -= 2 * Math.PI
  if (!cw && sweep <= 0) sweep += 2 * Math.PI
  const steps = Math.max(1, Math.ceil((Math.abs(sweep) * r) / ARC_STEP))
  let px = x0, py = y0
  for (let i = 1; i <= steps; i++) {
    const a = a0 + (sweep * i) / steps
    const qx = i === steps ? x1 : cx + r * Math.cos(a)
    const qy = i === steps ? y1 : cy + r * Math.sin(a)
    seg(px, py, qx, qy, z)
    px = qx
    py = qy
  }
}
