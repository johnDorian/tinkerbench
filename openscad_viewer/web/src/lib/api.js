// Thin wrapper around the REST API (internal/server).

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
  deleteFile: (name, file) => req('DELETE', `${p(name)}/files/${encodeURIComponent(file)}`),
  render: (name, rawParams) => req('POST', `${p(name)}/render`, rawParams ? { raw_params: rawParams } : {}),
  history: (name) => req('GET', `${p(name)}/history`),
  version: (name, n) => req('GET', `${p(name)}/history/${n}`),
  restore: (name, n) => req('POST', `${p(name)}/history/${n}/restore`),
  stlUrl: (name) => `${p(name)}/model.stl`,
  // Labelled parts of the model (see parts.js); a 404 or error means "no parts" to the viewer.
  parts: (name) => req('GET', `${p(name)}/parts`),
  partStlUrl: (name, part) => `${p(name)}/parts/${encodeURIComponent(part)}.stl`,
  images: (name) => req('GET', `${p(name)}/images`),
  // w shrinks the image server-side: a 1200 dpi scan is far too big for a thumbnail.
  imageUrl: (name, file, w) => `${p(name)}/images/${encodeURIComponent(file)}${w ? `?w=${w}` : ''}`,
  uploadImages: async (name, files) => {
    const form = new FormData()
    for (const f of files) form.append('file', f)
    const res = await fetch(`${p(name)}/images`, { method: 'POST', body: form })
    const data = await res.json().catch(() => null)
    if (!res.ok) throw new Error(data?.error || `${res.status} ${res.statusText}`)
    return data
  },
  deleteImage: (name, file) => req('DELETE', `${p(name)}/images/${encodeURIComponent(file)}`),
  // Printable measuring mats (not per project): [{name, title, paper, inner_mm, url}].
  mats: () => req('GET', '/api/mats'),
  measure: (name, body) => req('POST', `${p(name)}/measure`, body),
  addComment: (name, c) => req('POST', `${p(name)}/comments`, c),
  reply: (name, id, body) => req('POST', `${p(name)}/comments/${id}/replies`, { body }),
  resolve: (name, id, resolved) => req('POST', `${p(name)}/comments/${id}/resolve`, { resolved }),
  applySuggestion: (name, id) => req('POST', `${p(name)}/comments/${id}/apply`),
  deleteComment: (name, id) => req('DELETE', `${p(name)}/comments/${id}`),
}

// Who wrote something: the web UI is "you", anything else is the agent's name.
export const who = (author) => (author === 'web' ? 'You' : author || '?')

// OpenSCAD messages cite `in file "main.scad", line 5` (or without quotes); returns {file: Set(lines)}.
export function errorLines(messages = []) {
  const out = {}
  for (const m of messages) {
    for (const [, file, line] of m.matchAll(/in file "?([^",]+?)"?, line (\d+)/g)) {
      const f = file.split('/').pop()
      ;(out[f] ??= new Set()).add(Number(line))
    }
  }
  return out
}

export function ago(t) {
  const s = Math.round((Date.now() - new Date(t)) / 1000)
  if (s < 60) return `${Math.max(s, 0)}s ago`
  if (s < 3600) return `${Math.round(s / 60)} min ago`
  if (s < 86400) return `${Math.round(s / 3600)} h ago`
  return new Date(t).toLocaleDateString()
}
