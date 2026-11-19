// A small Markdown renderer for the wiring notes: headings, paragraphs, bold, italic, inline and
// fenced code, bullet and numbered lists, block quotes, tables and horizontal rules. Everything is
// HTML-escaped first, so the result is safe for {@html}.
const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

function inline(s) {
  const codes = []
  s = esc(s).replace(/`([^`]+)`/g, (_, c) => { codes.push(c); return `\u0000${codes.length - 1}\u0000` })
  s = s.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>').replace(/(^|[\s(])\*([^*\s][^*]*)\*(?=[\s).,;:]|$)/g, '$1<em>$2</em>')
  return s.replace(/\u0000(\d+)\u0000/g, (_, i) => `<code>${codes[+i]}</code>`)
}

const cells = (line) => line.trim().replace(/^\||\|$/g, '').split(/(?<!\\)\|/).map((c) => c.trim().replace(/\\\|/g, '|'))

export function renderMarkdown(src) {
  const lines = (src ?? '').replace(/\r\n?/g, '\n').replace(/<!--[\s\S]*?-->/g, '').split('\n')
  const out = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]
    if (!line.trim()) { i++; continue }
    if (/^```/.test(line)) {
      const buf = []
      for (i++; i < lines.length && !/^```/.test(lines[i]); i++) buf.push(lines[i])
      i++
      out.push(`<pre>${esc(buf.join('\n'))}</pre>`)
      continue
    }
    let m = line.match(/^(#{1,6})\s+(.*)$/)
    if (m) { const n = Math.min(m[1].length + 1, 6); out.push(`<h${n}>${inline(m[2])}</h${n}>`); i++; continue }
    if (/^(-{3,}|\*{3,})\s*$/.test(line)) { out.push('<hr>'); i++; continue }
    if (/^\s*\|/.test(line) && /^\s*\|?\s*:?-{2,}/.test(lines[i + 1] ?? '')) {
      const head = cells(line)
      i += 2
      const rows = []
      for (; i < lines.length && /^\s*\|/.test(lines[i]); i++) rows.push(cells(lines[i]))
      out.push('<table><thead><tr>' + head.map((c) => `<th>${inline(c)}</th>`).join('') + '</tr></thead><tbody>' +
        rows.map((r) => '<tr>' + r.map((c) => `<td>${inline(c)}</td>`).join('') + '</tr>').join('') + '</tbody></table>')
      continue
    }
    if (/^>\s?/.test(line)) {
      const buf = []
      for (; i < lines.length && /^>\s?/.test(lines[i]); i++) buf.push(lines[i].replace(/^>\s?/, ''))
      out.push(`<blockquote>${inline(buf.join(' '))}</blockquote>`)
      continue
    }
    m = line.match(/^\s*([-*]|\d+\.)\s+(.*)$/)
    if (m) {
      const ordered = /\d/.test(m[1])
      const items = []
      for (; i < lines.length; i++) {
        const mm = lines[i].match(/^\s*([-*]|\d+\.)\s+(.*)$/)
        if (mm && /\d/.test(mm[1]) === ordered) items.push(mm[2])
        else if (/^\s{2,}\S/.test(lines[i]) && items.length) items[items.length - 1] += ' ' + lines[i].trim()
        else break
      }
      const tag = ordered ? 'ol' : 'ul'
      out.push(`<${tag}>` + items.map((t) => `<li>${inline(t)}</li>`).join('') + `</${tag}>`)
      continue
    }
    const buf = []
    for (; i < lines.length && lines[i].trim() && !/^(#{1,6}\s|```|>|\s*([-*]|\d+\.)\s|\s*\|)/.test(lines[i]); i++) buf.push(lines[i].trim())
    if (!buf.length) { buf.push(line.trim()); i++ }
    out.push(`<p>${inline(buf.join(' '))}</p>`)
  }
  return out.join('\n')
}
