// Plate geometry in the server's frame: bed origin front-left, X right, Y away from the viewer, Z up, mm.
// The server (Go) does the same maths for auto-placement and the fit check; keep them in step.
import * as THREE from 'three'

const DEG = Math.PI / 180

/**
 * Model space -> bed space, without the position: move the model's bbox centre to the origin, scale about it,
 * then rotate X, then Y, then Z (Rz·Ry·Rx·S·T(-c)).
 */
export function modelMatrix(center, rotation, scale) {
  const [rx, ry, rz] = rotation
  const r = new THREE.Matrix4()
    .makeRotationZ(rz * DEG)
    .multiply(new THREE.Matrix4().makeRotationY(ry * DEG))
    .multiply(new THREE.Matrix4().makeRotationX(rx * DEG))
  const t = new THREE.Matrix4().makeTranslation(-center.x, -center.y, -center.z)
  return new THREE.Matrix4().makeScale(scale, scale, scale).multiply(r).multiply(t)
}

/** Bounds of the geometry after matrix m (min/max as Vector3). */
export function transformedBounds(geom, m) {
  const pos = geom.attributes.position
  const v = new THREE.Vector3()
  const min = new THREE.Vector3(Infinity, Infinity, Infinity)
  const max = new THREE.Vector3(-Infinity, -Infinity, -Infinity)
  for (let i = 0; i < pos.count; i++) {
    v.fromBufferAttribute(pos, i).applyMatrix4(m)
    min.min(v)
    max.max(v)
  }
  return { min, max }
}

/** Final matrix: footprint bbox centre at (x, y), lowest point at z = 0. */
export function placementMatrix(m0, bounds, x, y) {
  const cx = (bounds.min.x + bounds.max.x) / 2
  const cy = (bounds.min.y + bounds.max.y) / 2
  return new THREE.Matrix4().makeTranslation(x - cx, y - cy, -bounds.min.z).multiply(m0)
}

/** Printable area as {x0, x1, y0, y1, h, shape}. Uses the bbox of the shape when one is given. */
export function bedBox(bed) {
  const shape = bed?.shape?.length ? bed.shape : null
  const h = bed?.max_height ?? 260
  if (shape) {
    const xs = shape.map((p) => p[0])
    const ys = shape.map((p) => p[1])
    return { x0: Math.min(...xs), x1: Math.max(...xs), y0: Math.min(...ys), y1: Math.max(...ys), h, shape }
  }
  return { x0: 0, x1: bed?.width ?? 250, y0: 0, y1: bed?.depth ?? 250, h, shape: null }
}

/** True when the placed footprint and height are inside the printable area. */
export function fitsBed(box, bounds, x, y) {
  const hw = (bounds.max.x - bounds.min.x) / 2
  const hd = (bounds.max.y - bounds.min.y) / 2
  const h = bounds.max.z - bounds.min.z
  const e = 0.01
  return (
    x - hw >= box.x0 - e && x + hw <= box.x1 + e &&
    y - hd >= box.y0 - e && y + hd <= box.y1 + e &&
    h <= box.h + e
  )
}
