// Fetches and parses a plate's G-code off the main thread; the position buffers are transferred back.
import { parseGcode } from './gcode.js'

self.onmessage = async ({ data: { id, url } }) => {
  try {
    const res = await fetch(url, { cache: 'no-cache' })
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    const out = parseGcode(await res.text())
    self.postMessage({ id, ...out }, out.features.map((f) => f.positions.buffer))
  } catch (e) {
    self.postMessage({ id, error: String(e.message || e) })
  }
}
