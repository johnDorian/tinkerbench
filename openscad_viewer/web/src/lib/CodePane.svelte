<script>
  // Source view: file tabs, highlighted code with line numbers and error markers, and an editor.
  import { tick } from 'svelte'
  import hljs from 'highlight.js/lib/core'
  import openscad from 'highlight.js/lib/languages/openscad'
  import CommentThread from './CommentThread.svelte'

  hljs.registerLanguage('openscad', openscad)

  /**
   * files: [{name, content}]; selected: file name (bindable); marks: {file: Set(lines)};
   * changed: Set of files the agent just changed (tab highlight); onsave(file, content).
   * preview: {label, content} shows an old version read-only instead of the selected file.
   * comments: all comments of the project; on: comment actions {add, reply, resolve, apply, remove}.
   * focus: {file, line, n} scrolls to that line (n changes on every request).
   * onshow(comment): shows an anchored comment in the 3D view (passed on to the threads).
   */
  let {
    files = [], selected = $bindable(), marks = {}, changed = new Set(), preview = null,
    comments = [], on = {}, focus = null, onshow = null, onsave, onclosepreview, onrestore,
  } = $props()

  let editing = $state(false)
  let draft = $state('')
  let saving = $state(false)

  const current = $derived(files.find((f) => f.name === selected) ?? files[0])
  const shown = $derived(preview ? preview.content : current?.content ?? '')
  const dirty = $derived(editing && current && draft !== current.content)
  const html = $derived(hljs.highlight(shown, { language: 'openscad' }).value)
  const lines = $derived(shown.split('\n').length)
  const errLines = $derived(preview ? new Set() : marks[current?.name] ?? new Set())
  const rows = $derived(splitHtml(html))

  // Open comments of this file, grouped under their last line.
  const open = $derived(preview ? [] : comments.filter((c) => c.file === current?.name && !c.resolved))
  const threadsAt = $derived.by(() => {
    const at = {}
    for (const c of open) (at[Math.min(c.end_line, lines)] ??= []).push(c)
    return at
  })
  const commented = $derived.by(() => {
    const set = new Set()
    for (const c of open) for (let l = c.line; l <= Math.min(c.end_line, lines); l++) set.add(l)
    return set
  })

  // hljs returns one HTML string whose spans can cross lines (block comments); close and
  // reopen them at each newline so every line is valid HTML on its own.
  function splitHtml(h) {
    const out = []
    const openTags = []
    let cur = ''
    let last = 0
    const re = /(<span[^>]*>)|(<\/span>)|(\n)/g
    for (let m; (m = re.exec(h)); ) {
      cur += h.slice(last, m.index)
      last = re.lastIndex
      if (m[1]) {
        openTags.push(m[1])
        cur += m[1]
      } else if (m[2]) {
        openTags.pop()
        cur += m[2]
      } else {
        out.push(cur + '</span>'.repeat(openTags.length))
        cur = openTags.join('')
      }
    }
    out.push(cur + h.slice(last))
    return out
  }

  // ---- line selection and the comment composer ----
  let sel = $state(null) // {start, end}
  let anchor = 0
  let draftComment = $state('')
  let suggesting = $state(false)
  let suggestion = $state('')
  let posting = $state(false)
  let codeEl = $state()

  function clickLine(e, n) {
    if (preview) return
    if (e.shiftKey && sel) {
      sel = { start: Math.min(anchor, n), end: Math.max(anchor, n) }
    } else {
      anchor = n
      sel = { start: n, end: n }
      draftComment = ''
      suggesting = false
    }
    suggestion = shown.split('\n').slice(sel.start - 1, sel.end).join('\n')
  }

  function cancelComment() {
    sel = null
    draftComment = ''
    suggesting = false
  }

  async function postComment() {
    if (!draftComment.trim() && !suggesting) return
    posting = true
    try {
      const ok = await on.add({
        file: current.name, line: sel.start, end_line: sel.end,
        body: draftComment, suggestion: suggesting ? suggestion : undefined,
      })
      if (ok) cancelComment()
    } finally {
      posting = false
    }
  }

  function composerKey(e) {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') postComment()
    if (e.key === 'Escape') cancelComment()
  }

  // Reset the selection when switching files (not when the same file is reloaded).
  const currentName = $derived(current?.name)
  $effect(() => {
    currentName
    sel = null
  })

  $effect(() => {
    if (!focus) return
    focus.n
    tick().then(() => codeEl?.querySelector(`[data-line="${focus.line}"]`)?.scrollIntoView({ block: 'center', behavior: 'smooth' }))
  })

  // Content changed underneath the editor (agent edit) while there are no local changes: follow it.
  let editedFrom = $state(null)
  $effect(() => {
    if (editing && current && current.content !== editedFrom && draft === editedFrom) {
      draft = editedFrom = current.content
    }
  })

  function startEdit() {
    draft = editedFrom = current.content
    editing = true
  }

  async function save() {
    if (!dirty) return
    saving = true
    try {
      await onsave(current.name, draft)
      editedFrom = draft
    } finally {
      saving = false
    }
  }

  function keydown(e) {
    if ((e.ctrlKey || e.metaKey) && e.key === 's') {
      e.preventDefault()
      save()
    }
    if (e.key === 'Tab') {
      e.preventDefault()
      const t = e.target
      const [a, b] = [t.selectionStart, t.selectionEnd]
      draft = draft.slice(0, a) + '    ' + draft.slice(b)
      requestAnimationFrame(() => t.setSelectionRange(a + 4, a + 4))
    }
  }

  function select(name) {
    if (dirty && !confirm('Discard unsaved changes?')) return
    editing = false
    selected = name
  }

  const openIn = (file) => comments.filter((c) => c.file === file && !c.resolved).length
</script>

<div class="pane">
  <div class="tabs">
    {#each files as f (f.name)}
      <button class="tab" class:active={f.name === current?.name} class:changed={changed.has(f.name)} onclick={() => select(f.name)}>
        {f.name}
        {#if marks[f.name]?.size}<span class="dot" title="has errors"></span>{/if}
        {#if openIn(f.name)}<span class="count" title="open comments">{openIn(f.name)}</span>{/if}
      </button>
    {/each}
    <span class="spacer"></span>
    {#if !preview && current}
      {#if editing}
        <button onclick={save} disabled={!dirty || saving} class="primary">{saving ? 'Saving…' : 'Save & render'}</button>
        <button onclick={() => { if (!dirty || confirm('Discard unsaved changes?')) editing = false }}>Close editor</button>
      {:else}
        <button onclick={startEdit}>Edit</button>
      {/if}
    {/if}
  </div>

  {#if preview}
    <div class="banner">
      <span>{preview.label}</span>
      <span class="spacer"></span>
      <button class="primary" onclick={onrestore}>Restore this version</button>
      <button onclick={onclosepreview}>Back to current</button>
    </div>
  {/if}

  {#if editing && !preview}
    <textarea class="code editor" bind:value={draft} onkeydown={keydown} spellcheck="false"></textarea>
    {#if current && draft !== editedFrom && current.content !== editedFrom}
      <div class="banner warn">The file was changed by someone else while you were editing. Saving will overwrite it.</div>
    {/if}
  {:else}
    <div class="code view" bind:this={codeEl}>
      {#each rows as row, i}
        {@const n = i + 1}
        <div class="row" data-line={n} class:sel={sel && n >= sel.start && n <= sel.end} class:commented={commented.has(n)}>
          <button class="ln" class:err={errLines.has(n)} onclick={(e) => clickLine(e, n)}
            title={preview ? '' : 'Comment on this line (shift-click for a range)'} tabindex="-1">{n}</button>
          <code class="hljs line">{@html row || ' '}</code>
        </div>
        {#each threadsAt[n] ?? [] as c (c.id)}
          <CommentThread comment={c} {on} {onshow} />
        {/each}
        {#if sel && sel.end === n}
          <div class="composer">
            <div class="muted">Comment on {sel.end > sel.start ? `lines ${sel.start}–${sel.end}` : `line ${sel.start}`}</div>
            <!-- svelte-ignore a11y_autofocus -->
            <textarea bind:value={draftComment} onkeydown={composerKey} rows="2" autofocus
              placeholder="What should change here? (Ctrl+Enter to post, Esc to cancel)"></textarea>
            <label class="muted"><input type="checkbox" bind:checked={suggesting}> Suggest replacement code</label>
            {#if suggesting}
              <textarea class="mono" bind:value={suggestion} onkeydown={composerKey} rows={Math.min(Math.max(suggestion.split('\n').length, 2), 14)} spellcheck="false"></textarea>
            {/if}
            <div class="actions">
              <button class="primary" disabled={posting || (!draftComment.trim() && !suggesting)} onclick={postComment}>Comment</button>
              <button onclick={cancelComment}>Cancel</button>
            </div>
          </div>
        {/if}
      {/each}
    </div>
  {/if}
</div>

<style>
  .pane { display: flex; flex-direction: column; min-height: 0; height: 100%; }
  .tabs {
    display: flex; gap: 2px; align-items: center; padding: 6px 8px; border-bottom: 1px solid var(--line);
    overflow-x: auto; flex-shrink: 0;
  }
  .tab {
    border: 0; background: none; color: var(--muted); padding: 4px 10px; border-radius: 6px;
    font: 13px var(--mono); cursor: pointer; display: flex; align-items: center; gap: 6px; white-space: nowrap;
  }
  .tab.active { background: var(--accent-soft); color: var(--text); }
  .tab.changed { animation: flash 1.5s ease-out; }
  @keyframes flash { from { background: var(--flash); } }
  .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--err); }
  .banner {
    display: flex; gap: 8px; align-items: center; padding: 6px 10px; font-size: 13px;
    background: var(--accent-soft); border-bottom: 1px solid var(--line); flex-shrink: 0;
  }
  .banner.warn { background: var(--warn-soft); border-top: 1px solid var(--line); }
  .code { flex: 1; min-height: 0; font: 13px/1.55 var(--mono); }
  .view { overflow: auto; padding: 10px 0; }
  .row { display: flex; }
  .ln {
    flex-shrink: 0; width: 3.2em; padding: 0 8px 0 0; text-align: right; color: var(--faint); font: inherit;
    background: none; border: 0; border-right: 1px solid var(--line); border-radius: 0; cursor: pointer; user-select: none;
  }
  .ln:hover { color: var(--accent); background: var(--accent-soft); }
  .ln.err { color: #fff; background: var(--err); }
  .row.commented .ln { box-shadow: inset 3px 0 var(--accent); }
  .row.sel { background: var(--accent-soft); }
  .line { flex: 1; padding: 0 14px; white-space: pre-wrap; overflow-wrap: anywhere; }
  code { font: inherit; background: none; }
  .count {
    font: 11px var(--sans); background: var(--accent); color: var(--on-accent); border-radius: 999px; padding: 0 6px;
  }
  .composer {
    margin: 4px 10px 6px 3.6em; padding: 8px 10px; border: 1px solid var(--accent); border-radius: 6px;
    background: var(--bg); display: flex; flex-direction: column; gap: 6px; font: 13px var(--sans);
  }
  .composer textarea {
    width: 100%; font: 13px var(--sans); background: var(--card); color: var(--text);
    border: 1px solid var(--line); border-radius: 6px; padding: 6px; resize: vertical;
  }
  .composer textarea.mono { font: 12px/1.45 var(--mono); tab-size: 4; }
  .composer label { display: flex; gap: 6px; align-items: center; }
  .composer .actions { display: flex; gap: 6px; }
  .muted { color: var(--muted); font-size: 12px; }
  .editor {
    width: 100%; border: 0; resize: none; padding: 10px 14px; background: var(--card); color: var(--text);
    outline: none; tab-size: 4; white-space: pre; box-sizing: border-box;
  }
</style>
