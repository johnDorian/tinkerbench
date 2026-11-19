async function req(method, path, body) {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (res.status === 204) return null
  const data = await res.json().catch(() => null)
  if (!res.ok) throw new Error(data?.error || `${res.status} ${res.statusText}`)
  return data
}

const p = (name) => `/api/projects/${encodeURIComponent(name)}`

export const api = {
  config: () => req('GET', '/api/config'),
  projects: () => req('GET', '/api/projects'),
  project: (name) => req('GET', p(name)),
  create: (name, description) => req('POST', '/api/projects', { name, description }),
  remove: (name) => req('DELETE', p(name)),
  saveFile: (name, file, content) => req('PUT', `${p(name)}/files/${encodeURIComponent(file)}`, { content }),
  render: (name) => req('POST', `${p(name)}/render`, {}),
  wiring: (name, view) => req('GET', `${p(name)}/wiring?view=${view || ''}`),
  wiringMarkdownUrl: (name, view, download) => `${p(name)}/wiring.md?view=${view || ''}${download ? '&download=1' : ''}`,
  wiringMarkdown: async (name, view) => {
    const res = await fetch(`${p(name)}/wiring.md?view=${view || ''}`)
    if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
    return res.text()
  },
  saveNotes: (name, text, view) => req('PUT', `${p(name)}/notes?view=${view || ''}`, { text }),
  viewUrl: (name, view, v) => `${p(name)}/views/${view}.svg?v=${v}`,
  downloadUrl: (name) => `${p(name)}/download.fzz`,
  addPart: (name, module_id) => req('POST', `${p(name)}/parts`, { module_id }),
  removePart: (name, title) => req('DELETE', `${p(name)}/parts/${encodeURIComponent(title)}`),
  history: (name) => req('GET', `${p(name)}/history`),
  version: (name, n) => req('GET', `${p(name)}/history/${n}`),
  restore: (name, n) => req('POST', `${p(name)}/history/${n}/restore`),
  search: (q, source) => req('GET', `/api/parts?${new URLSearchParams({ q, source: source || '', limit: 40 })}`),
  importUrl: (url, overwrite) => req('POST', '/api/userparts', { url, overwrite }),
  importFile: async (file, overwrite) => {
    const res = await fetch(`/api/userparts${overwrite ? '?overwrite=1' : ''}`, { method: 'POST', headers: { 'Content-Type': 'application/octet-stream' }, body: file })
    const data = await res.json().catch(() => null)
    if (!res.ok) throw new Error(data?.error || `${res.status} ${res.statusText}`)
    return data
  },
  deleteUserPart: (id, force) => req('DELETE', `/api/userparts/${encodeURIComponent(id)}${force ? '?force=1' : ''}`),
  part: (id) => req('GET', `/api/parts/${encodeURIComponent(id)}`),
  partImage: (id, view) => `/api/parts/${encodeURIComponent(id)}/${view}.svg`,
}

export const who = (a) => (a === 'web' ? 'You' : a || '?')

export function ago(t) {
  const s = Math.round((Date.now() - new Date(t)) / 1000)
  if (s < 60) return `${Math.max(s, 0)}s ago`
  if (s < 3600) return `${Math.round(s / 60)} min ago`
  if (s < 86400) return `${Math.round(s / 3600)} h ago`
  return new Date(t).toLocaleDateString()
}
