<script>
  // One comment with its suggestion, replies and actions.
  import { ago, who } from './api.js'

  /**
   * comment: project.Comment; on: {reply(id, body), resolve(id, bool), apply(id), remove(id)};
   * onshow(comment): optional, shows a button for comments with a 3D anchor (showLabel is its text).
   */
  let { comment: c, on, compact = false, onshow = null, showLabel = 'Show in 3D' } = $props()

  let replying = $state(false)
  let reply = $state('')
  let busy = $state(false)

  // Model-only comments (anchor, no source line) say where on the model they are instead.
  const range = $derived(
    !c.file
      ? `${c.anchor?.part ? `part ${c.anchor.part}` : 'whole model'} · ${c.anchor?.kind ?? 'model'}`
      : c.end_line > c.line ? `lines ${c.line}–${c.end_line}` : `line ${c.line}`,
  )

  async function act(fn) {
    busy = true
    try {
      await fn()
    } finally {
      busy = false
    }
  }

  async function send() {
    if (!reply.trim()) return
    await act(() => on.reply(c.id, reply))
    reply = ''
    replying = false
  }

  function keydown(e) {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') send()
    if (e.key === 'Escape') replying = false
  }
</script>

<div class="thread" class:resolved={c.resolved} class:compact>
  <div class="head">
    <b>{who(c.author)}</b>
    <span class="muted">{range} · {ago(c.created)}</span>
    {#if c.outdated && !c.resolved}<span class="tag warn" title="The commented code has changed since">outdated</span>{/if}
    {#if c.anchor?.stale && !c.resolved}<span class="tag warn" title="The geometry changed; the pin may not sit on the same spot">model changed</span>{/if}
    {#if c.resolved}<span class="tag ok">resolved{c.resolved_by ? ` by ${who(c.resolved_by)}` : ''}</span>{/if}
  </div>

  {#if (c.outdated || compact) && c.quote}
    <pre class="quote">{c.quote}</pre>
  {/if}
  {#if c.body}<div class="body">{c.body}</div>{/if}

  {#if c.suggestion != null}
    <div class="suggestion">
      <div class="label">Suggested change</div>
      <pre class="old">{c.quote}</pre>
      <pre class="new">{c.suggestion || '(delete these lines)'}</pre>
    </div>
  {/if}

  {#each c.replies as r}
    <div class="reply">
      <b>{who(r.author)}</b> <span class="muted">{ago(r.time)}</span>
      <div class="body">{r.body}</div>
    </div>
  {/each}

  {#if replying}
    <!-- svelte-ignore a11y_autofocus -->
    <textarea bind:value={reply} onkeydown={keydown} rows="2" placeholder="Reply… (Ctrl+Enter)" autofocus></textarea>
  {/if}

  <div class="actions">
    {#if replying}
      <button class="primary" disabled={busy || !reply.trim()} onclick={send}>Reply</button>
      <button onclick={() => (replying = false)}>Cancel</button>
    {:else}
      {#if onshow && c.anchor}
        <button onclick={() => onshow(c)} title="Show this comment on the model">{showLabel}</button>
      {/if}
      {#if !c.resolved}
        <button onclick={() => (replying = true)}>Reply</button>
        {#if c.suggestion != null && !c.outdated}
          <button class="primary" disabled={busy} onclick={() => act(() => on.apply(c.id))}>Apply suggestion</button>
        {/if}
        <button disabled={busy} onclick={() => act(() => on.resolve(c.id, true))}>Resolve</button>
      {:else}
        <button disabled={busy} onclick={() => act(() => on.resolve(c.id, false))}>Reopen</button>
      {/if}
      <span class="spacer"></span>
      <button class="link danger" disabled={busy} onclick={() => confirm('Delete this comment?') && act(() => on.remove(c.id))}>Delete</button>
    {/if}
  </div>
</div>

<style>
  .thread {
    font: 13px/1.45 system-ui, sans-serif; background: var(--bg); border: 1px solid var(--line);
    border-left: 3px solid var(--accent); border-radius: 6px; padding: 8px 10px; margin: 4px 10px 6px 3.6em;
    display: flex; flex-direction: column; gap: 6px; white-space: normal;
  }
  .thread.compact { margin: 0; }
  .thread.resolved { border-left-color: var(--line); opacity: 0.8; }
  .head { display: flex; gap: 8px; align-items: baseline; flex-wrap: wrap; }
  .muted { color: var(--muted); font-size: 12px; }
  .tag { font-size: 11px; padding: 0 6px; border-radius: 999px; }
  .tag.warn { background: var(--warn-soft); color: var(--warn); }
  .tag.ok { background: var(--ok-soft); color: var(--ok); }
  .body { white-space: pre-wrap; overflow-wrap: anywhere; }
  pre { margin: 0; font: 12px/1.45 var(--mono); white-space: pre-wrap; overflow-wrap: anywhere; padding: 4px 8px; border-radius: 4px; }
  .quote { background: var(--card); color: var(--muted); }
  .suggestion { display: flex; flex-direction: column; gap: 2px; }
  .label { font-size: 12px; color: var(--muted); }
  .old { background: var(--err-soft); text-decoration: line-through; text-decoration-color: color-mix(in srgb, var(--err) 50%, transparent); }
  .new { background: var(--ok-soft); }
  .reply { border-top: 1px solid var(--line); padding-top: 6px; }
  textarea {
    width: 100%; font: 13px system-ui, sans-serif; background: var(--card); color: var(--text);
    border: 1px solid var(--line); border-radius: 6px; padding: 6px; resize: vertical;
  }
  .actions { display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
  .actions button { font-size: 12px; padding: 2px 8px; }
  .spacer { flex: 1; }
  .link { background: none; border: 0; }
</style>
