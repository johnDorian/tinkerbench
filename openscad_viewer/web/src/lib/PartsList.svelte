<script>
  // Collapsible list of the labelled parts and the features over the 3D view.
  // Hovering a row highlights that part in the view; the checkbox hides it; the name jumps to its line.

  /**
   * parts: [{name, file, line, ok, error, triangles}] (already filtered to the shown set);
   * features: [{name, file, line}]; live: false when the parts are not shown as meshes (the list is
   * then for reading only); hidden: Set of part names; hovered: part name or null.
   * onhover(name|null), ontoggle(name), onjump({file, line}).
   */
  let { parts = [], features = [], live = true, hidden = new Set(), hovered = null, onhover, ontoggle, onjump } = $props()

  let open = $state(typeof matchMedia === 'undefined' || matchMedia('(min-width: 721px)').matches)

  const label = (name) => (name === '(unlabelled)' ? 'unlabelled' : name)
</script>

<div class="parts-panel" class:collapsed={!open}>
  <button class="head" onclick={() => (open = !open)} aria-expanded={open}>
    <span>Parts</span>
    {#if parts.length}<span class="count">{parts.length}</span>{/if}
    {#if features.length}<span class="count feat">{features.length} features</span>{/if}
    <span class="chev">{open ? '▾' : '▸'}</span>
  </button>
  {#if open}
    <ul>
      {#each parts as p (p.name)}
        <li class:hovered={hovered === p.name} class:off={hidden.has(p.name)}
          onmouseenter={() => live && onhover?.(p.name)} onmouseleave={() => onhover?.(null)}>
          <input type="checkbox" checked={!hidden.has(p.name)} disabled={!live || !p.ok || !p.triangles}
            onchange={() => ontoggle?.(p.name)} aria-label="Show {label(p.name)}" />
          {#if p.file}
            <button class="name" onclick={() => onjump?.(p)} title="Show {p.file}:{p.line} in the code">{label(p.name)}</button>
          {:else}
            <span class="name muted">{label(p.name)}</span>
          {/if}
          <span class="meta">{p.file ? `${p.file}:${p.line}` : ''}{p.ok && p.triangles ? ` · ${p.triangles.toLocaleString()} tris` : ''}</span>
        </li>
      {:else}
        <li class="muted">No parts</li>
      {/each}
      {#if features.length}
        <li class="section">Features</li>
        {#each features as f, i (i)}
          <li class="feature">
            <span class="num">{i + 1}</span>
            {#if f.file}
              <button class="name" onclick={() => onjump?.(f)} title="Show {f.file}:{f.line} in the code">{f.name}</button>
            {:else}
              <span class="name">{f.name}</span>
            {/if}
          </li>
        {/each}
      {/if}
    </ul>
  {/if}
</div>

<style>
  .parts-panel {
    position: absolute; top: 8px; right: 8px; z-index: 3; width: 220px; max-width: 55%;
    background: var(--card); border: 1px solid var(--line); border-radius: 8px; font-size: 12px;
    box-shadow: 0 2px 8px rgb(0 0 0 / 0.08); display: flex; flex-direction: column;
  }
  .parts-panel.collapsed { width: auto; }
  .head {
    display: flex; gap: 6px; align-items: center; width: 100%; border: 0; border-radius: 8px;
    background: none; padding: 5px 8px; font-size: 12px; text-align: left;
  }
  .count {
    background: var(--accent); color: #fff; border-radius: 999px; padding: 0 6px; font-size: 11px;
  }
  .count.feat { background: var(--line); color: var(--muted); }
  .chev { margin-left: auto; color: var(--muted); }
  ul {
    list-style: none; margin: 0; padding: 0 0 4px; overflow: auto; max-height: min(260px, 45vh);
  }
  li {
    display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 0 6px; align-items: center;
    padding: 3px 8px; border-top: 1px solid var(--line);
  }
  li.hovered { background: var(--accent-soft); }
  li.off .name { text-decoration: line-through; color: var(--faint); }
  li.section {
    display: block; color: var(--muted); font-size: 11px; text-transform: uppercase; letter-spacing: 0.04em;
    padding-top: 6px;
  }
  li.feature { grid-template-columns: auto minmax(0, 1fr); }
  .name {
    background: none; border: 0; padding: 0; text-align: left; font: inherit; color: var(--text);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: pointer; min-width: 0;
  }
  .name:disabled, span.name { cursor: default; }
  button.name:hover { color: var(--accent); text-decoration: underline; }
  .meta {
    grid-column: 2; font: 11px var(--mono); color: var(--faint); overflow: hidden; text-overflow: ellipsis;
    white-space: nowrap; min-width: 0;
  }
  li:not(.section):not(.feature) .meta:empty { display: none; }
  .num {
    display: inline-grid; place-items: center; width: 16px; height: 16px; border-radius: 50%;
    background: var(--card); border: 1px solid var(--accent); color: var(--accent); font-size: 10px;
  }
  .muted { color: var(--muted); }
  input { margin: 0; }
  @media (max-width: 720px) {
    .parts-panel { width: 170px; }
    .parts-panel.collapsed { width: auto; }
  }
</style>
