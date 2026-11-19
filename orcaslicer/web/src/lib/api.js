// Thin wrapper around the REST API (internal/server). Errors carry .status.

async function req(method, path, body) {
  const isForm = body instanceof FormData
  const res = await fetch(path, {
    method,
    headers: body === undefined || isForm ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : isForm ? body : JSON.stringify(body),
  })
  if (res.status === 204) return null
  const data = await res.json().catch(() => null)
  if (!res.ok) {
    const err = new Error(data?.error || `${res.status} ${res.statusText}`)
    err.status = res.status
    throw err
  }
  return data
}

const platePath = (id) => `/api/plates/${encodeURIComponent(id)}`
const modelPath = (id) => `/api/models/${encodeURIComponent(id)}`

export const api = {
  config: () => req('GET', '/api/config'),
  profiles: (machine) => req('GET', `/api/profiles?machine=${encodeURIComponent(machine)}`),
  preset: (kind, name) => req('GET', `/api/profiles/${kind}/${encodeURIComponent(name)}`),

  models: () => req('GET', '/api/models'),
  uploadModel: (file, name) => {
    const form = new FormData()
    form.append('file', file)
    if (name) form.append('name', name)
    return req('POST', '/api/models', form)
  },
  deleteModel: (id) => req('DELETE', modelPath(id)),
  modelUrl: (id, rev = 0) => `${modelPath(id)}/model.stl?v=${rev}`,

  openscadProjects: () => req('GET', '/api/openscad/projects'),
  importOpenscad: (project, plate) => req('POST', '/api/openscad/import', plate ? { project, plate } : { project }),

  plates: () => req('GET', '/api/plates'),
  plate: (id) => req('GET', platePath(id)),
  createPlate: (body) => req('POST', '/api/plates', body),
  savePlate: (p) => req('PUT', platePath(p.id), p),
  deletePlate: (id) => req('DELETE', platePath(id)),

  addObject: (pid, body) => req('POST', `${platePath(pid)}/objects`, body),
  patchObject: (pid, oid, body) => req('PATCH', `${platePath(pid)}/objects/${encodeURIComponent(oid)}`, body),
  removeObject: (pid, oid) => req('DELETE', `${platePath(pid)}/objects/${encodeURIComponent(oid)}`),
  duplicateObject: (pid, oid) => req('POST', `${platePath(pid)}/objects/${encodeURIComponent(oid)}/duplicate`),
  arrange: (pid, spacing = 6) => req('POST', `${platePath(pid)}/arrange`, { spacing }),

  slice: (pid) => req('POST', `${platePath(pid)}/slice`),
  sliceResult: (pid) => req('GET', `${platePath(pid)}/slice`),
  gcodeUrl: (pid, download) => `${platePath(pid)}/plate.gcode${download ? '?download' : ''}`,
  threemfUrl: (pid, download) => `${platePath(pid)}/plate.3mf${download ? '?download' : ''}`,
  print: (pid) => req('POST', `${platePath(pid)}/print`),

  printer: () => req('GET', '/api/printer'),
  printerAction: (action) => req('POST', `/api/printer/${action}`),
}

// "12:05" style duration from seconds.
export function hm(seconds) {
  if (!seconds && seconds !== 0) return '–'
  const total = Math.round(seconds / 60)
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')} h`
}

export function ago(t) {
  if (!t) return ''
  const s = Math.round((Date.now() - new Date(t)) / 1000)
  if (Number.isNaN(s)) return ''
  if (s < 60) return `${Math.max(s, 0)}s ago`
  if (s < 3600) return `${Math.round(s / 60)} min ago`
  if (s < 86400) return `${Math.round(s / 3600)} h ago`
  return new Date(t).toLocaleDateString()
}

export const dims = (size) => (size ? size.map((v) => Number(v).toFixed(1)).join(' × ') + ' mm' : '')
