<script>
  // Profile selects and the curated settings. An override in plate.overrides is highlighted; the reset button
  // drops it so the field falls back to the preset value.
  let { plate, profiles, presets, curated = [], beds = [], onprofile, onoverride } = $props()

  // Filament presets hold these; everything else in the curated list comes from the process preset.
  const FILAMENT_KEYS = new Set(['nozzle_temperature', 'hot_plate_temp'])

  const items = $derived(curated.map(normalize))

  function normalize(c) {
    if (typeof c === 'string') return { key: c, label: c, kind: 'text', options: [], source: null }
    return {
      key: c.key,
      label: c.label ?? c.key,
      kind: String(c.kind ?? c.type ?? 'text').split(' ')[0], // "number mm" -> number, "percent string" -> percent
      options: c.options ?? [],
      source: c.source ?? null,
      default: c.default,
      unit: c.unit ?? (String(c.kind ?? '').includes('mm') ? 'mm' : ''),
    }
  }

  const sourceOf = (item) => item.source ?? (FILAMENT_KEYS.has(item.key) ? 'filament' : 'process')

  function presetValue(item) {
    const v = presets?.[sourceOf(item)]?.[item.key]
    const s = Array.isArray(v) ? v[0] : v
    if (s !== undefined && s !== null) return String(s)
    return item.default !== undefined ? String(item.default) : ''
  }

  function optionList(item, current) {
    const list = (item.options ?? []).map((o) =>
      typeof o === 'string'
        ? { value: o, label: o }
        : Array.isArray(o)
          ? { value: String(o[0]), label: String(o[1] ?? o[0]) }
          : { value: String(o.value), label: String(o.label ?? o.value) },
    )
    if (current && !list.some((o) => o.value === current)) list.unshift({ value: current, label: current })
    return list
  }

  // "20" -> "20%"; anything else is kept as typed
  const percent = (raw) => (/^\d+(\.\d+)?$/.test(raw.trim()) ? `${raw.trim()}%` : raw.trim())

  function commit(item, raw) {
    if (raw === '' || raw === null || raw === undefined) return onoverride(item.key, undefined)
    onoverride(item.key, String(raw))
  }

  function withCurrent(list, name) {
    return name && !list.some((x) => x.name === name) ? [{ name }, ...list] : list
  }
</script>

<section class="panel">
  <h2>Profiles</h2>
  <label class="row">
    <span>Printer</span>
    <select value={plate.machine} onchange={(e) => onprofile('machine', e.currentTarget.value)}>
      {#each withCurrent(profiles.machines ?? [], plate.machine) as m (m.name)}<option value={m.name}>{m.name}</option>{/each}
    </select>
  </label>
  <label class="row">
    <span>Process</span>
    <select value={plate.process} onchange={(e) => onprofile('process', e.currentTarget.value)}>
      {#each withCurrent(profiles.processes ?? [], plate.process) as p (p.name)}
        <option value={p.name}>{p.name}{p.layer_height ? ` (${p.layer_height} mm)` : ''}</option>
      {/each}
    </select>
  </label>
  <label class="row">
    <span>Filament</span>
    <select value={plate.filament} onchange={(e) => onprofile('filament', e.currentTarget.value)}>
      {#each withCurrent(profiles.filaments ?? [], plate.filament) as f (f.name)}
        <option value={f.name}>{f.name}{f.type ? ` (${f.type})` : ''}</option>
      {/each}
    </select>
  </label>
  {#if plate.multi_bed}
    <label class="row">
      <span>Bed</span>
      <select value={plate.effective_bed} onchange={(e) => onprofile('bed', e.currentTarget.value)}>
        {#each withCurrent(beds.map((b) => ({ name: b })), plate.effective_bed) as b (b.name)}<option value={b.name}>{b.name}</option>{/each}
      </select>
    </label>
  {:else if plate.effective_bed}
    <p class="bedinfo">Bed: {plate.effective_bed}, set by the printer profile</p>
  {/if}

  <h2>Settings</h2>
  {#each items as item (item.key)}
    {@const ov = plate.overrides?.[item.key]}
    {@const val = ov ?? presetValue(item)}
    <div class="field" class:overridden={ov !== undefined}>
      <div class="label">
        <span>{item.label}</span>
        {#if ov !== undefined}
          <button class="reset" onclick={() => onoverride(item.key, undefined)} title="Reset to preset ({presetValue(item)})" aria-label="Reset {item.label} to preset">↺</button>
        {/if}
      </div>
      <div class="input">
        {#if item.kind === 'bool'}
          <input type="checkbox" checked={val === '1' || val === 'true'} onchange={(e) => commit(item, e.currentTarget.checked ? '1' : '0')} />
        {:else if item.kind === 'select'}
          <select value={val} onchange={(e) => commit(item, e.currentTarget.value)}>
            {#each optionList(item, val) as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
          </select>
        {:else if item.kind === 'percent'}
          <input type="text" value={val} onchange={(e) => commit(item, percent(e.currentTarget.value))} />
        {:else if item.kind === 'number' || item.kind === 'int'}
          <input type="number" step={item.kind === 'int' ? 1 : 'any'} value={val} onchange={(e) => commit(item, e.currentTarget.value)} />
        {:else}
          <input type="text" value={val} onchange={(e) => commit(item, e.currentTarget.value)} />
        {/if}
        {#if item.unit && item.kind !== 'bool' && item.kind !== 'select'}<span class="unit">{item.unit}</span>{/if}
      </div>
      <div class="hint">preset: {presetValue(item) || '–'}</div>
    </div>
  {:else}
    <p class="muted">No curated settings from the server.</p>
  {/each}
</section>

<style>
  .panel { padding: 10px 12px; border-bottom: 1px solid var(--line); display: flex; flex-direction: column; gap: 6px; }
  h2 { font-size: 13px; margin: 6px 0 2px; text-transform: uppercase; letter-spacing: 0.04em; color: var(--muted); }
  h2:first-child { margin-top: 0; }
  .row { display: grid; grid-template-columns: 70px minmax(0, 1fr); gap: 6px; align-items: center; font-size: 13px; }
  .row select { width: 100%; min-width: 0; }
  .field {
    display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1.1fr); gap: 2px 8px; align-items: center;
    padding: 4px 6px; border-radius: 6px; border: 1px solid transparent;
  }
  .field.overridden { background: var(--accent-soft); border-color: var(--accent); }
  .label { display: flex; gap: 4px; align-items: center; font-size: 13px; min-width: 0; }
  .label span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .reset { font-size: 12px; padding: 0 5px; line-height: 1.4; }
  .input { display: flex; gap: 4px; align-items: center; min-width: 0; }
  .input input[type="text"], .input input[type="number"], .input select { width: 100%; min-width: 0; }
  .input input[type="checkbox"] { width: 16px; height: 16px; }
  .unit { font-size: 12px; color: var(--muted); }
  .hint { grid-column: 1 / -1; font-size: 11px; color: var(--faint); }
  .muted { color: var(--muted); font-size: 13px; }
  .bedinfo { margin: 0; font-size: 12px; color: var(--muted); }
</style>
