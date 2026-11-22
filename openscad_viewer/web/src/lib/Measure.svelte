<script>
  // Measuring dialog: pick how the scale is known, measure, and check the result against
  // the overlay before trusting the numbers.
  import { api } from './api.js'

  let { project, image = null, onclose } = $props()

  // How the millimetres are established. A scan records its own dpi, which is why it is
  // the default and the easiest path.
  const SCALES = [
    { id: 'file', label: 'The dpi stored in the file', hint: 'Scanners record it, phone cameras do not.' },
    { id: 'dpi', label: 'Scan dpi', unit: 'dpi', hint: 'For a scanner that does not write it.', value: 1200 },
    { id: 'grid', label: 'Graph paper pitch', unit: 'mm', hint: 'A photo on squared paper; the scale comes from the grid.', value: 1 },
    { id: 'ref', label: 'A length I already know', unit: 'mm', hint: 'The overall length, measured with a caliper.', value: 10 },
  ]

  let scaleId = $state('file')
  let scaleValue = $state(1200)
  let channel = $state('gray')
  let invert = $state(false)
  let flip = $state(false)
  let tol = $state(0.08)
  let minStep = $state(0.5)
  let thickness = $state(0)
  let cameraMM = $state(0)

  let busy = $state(false)
  let error = $state('')
  let result = $state(null)
  let copied = $state(false)
  let dlg

  const scale = $derived(SCALES.find((s) => s.id === scaleId))

  $effect(() => {
    if (image && dlg && !dlg.open) {
      result = null
      error = ''
      dlg.showModal()
    }
  })

  function pickScale(id) {
    scaleId = id
    const s = SCALES.find((x) => x.id === id)
    if (s.value !== undefined) scaleValue = s.value
  }

  async function run() {
    busy = true
    error = ''
    try {
      result = await api.measure(project, {
        image: image.name,
        dpi: scaleId === 'dpi' ? Number(scaleValue) : 0,
        grid_mm: scaleId === 'grid' ? Number(scaleValue) : 0,
        scale_ref_mm: scaleId === 'ref' ? Number(scaleValue) : 0,
        thickness_mm: Number(thickness),
        camera_mm: Number(cameraMM),
        invert,
        flip,
        channel,
        tol_mm: Number(tol),
        min_step_mm: Number(minStep),
        overlay: true,
      })
    } catch (e) {
      error = e.message
      result = null
    } finally {
      busy = false
    }
  }

  function copy() {
    navigator.clipboard?.writeText(result.openscad).then(() => {
      copied = true
      setTimeout(() => (copied = false), 1500)
    })
  }

  function close() {
    dlg?.close()
    onclose?.()
  }

  const shown = $derived(result?.overlay ? `data:image/png;base64,${result.overlay}` : image ? api.imageUrl(project, image.name, 1400) : null)
</script>

<dialog bind:this={dlg} onclose={() => onclose?.()}>
  {#if image}
    <header>
      <strong>Measure {image.name}</strong>
      <span class="muted">{image.width}&times;{image.height} px{image.dpi ? ` · ${Math.round(image.dpi)} dpi in the file` : ' · no dpi in the file'}</span>
      <span class="spacer"></span>
      <button onclick={close}>Close</button>
    </header>

    <div class="body">
      <div class="picture">
        {#if shown}<img src={shown} alt="the part, with what was measured drawn over it" />{/if}
        {#if result}
          <p class="legend">
            <span class="key outline"></span> the outline found
            <span class="key step"></span> step boundaries
            <span class="key tip"></span> the circle fitted to the round end
          </p>
        {/if}
      </div>

      <div class="side-panel">
        <fieldset>
          <legend>How is the scale known?</legend>
          {#each SCALES as s}
            <label class="radio">
              <input type="radio" checked={scaleId === s.id} onchange={() => pickScale(s.id)} />
              <span>{s.label}</span>
            </label>
          {/each}
          {#if scale.unit}
            <label class="num">
              <input type="number" bind:value={scaleValue} step="any" min="0" />
              <span>{scale.unit}</span>
            </label>
          {/if}
          <p class="hint">{scale.hint}</p>
        </fieldset>

        <details>
          <summary>If the outline comes out wrong</summary>
          <label class="check"><input type="checkbox" bind:checked={invert} /> The part is lighter than the background</label>
          <label class="check"><input type="checkbox" bind:checked={flip} /> Start from the other end</label>
          <label class="field">
            <span>Threshold on</span>
            <select bind:value={channel}>
              <option value="gray">brightness (a scan)</option>
              <option value="a">colour a* (shadows)</option>
              <option value="b">colour b* (shadows)</option>
              <option value="sat">saturation</option>
            </select>
          </label>
          <label class="field"><span>Width tolerance within one step</span><input type="number" bind:value={tol} step="0.01" min="0" /></label>
          <label class="field"><span>Shortest step to report</span><input type="number" bind:value={minStep} step="0.1" min="0" /></label>
          <label class="field"><span>Part thickness (photo parallax)</span><input type="number" bind:value={thickness} step="0.1" min="0" /></label>
          <label class="field"><span>Lens-to-paper distance</span><input type="number" bind:value={cameraMM} step="10" min="0" /></label>
        </details>

        <button class="primary" onclick={run} disabled={busy}>{busy ? 'Measuring…' : 'Measure'}</button>
        {#if error}<p class="error">{error}</p>{/if}

        {#if result}
          <div class="result">
            <p class="overall">
              <strong>{result.overall_length_mm} &times; {result.overall_width_mm} mm</strong>
              <span class="muted">scale {result.scale_mm_per_px} mm/px, from {result.scale_from}</span>
            </p>
            <table>
              <thead><tr><th>#</th><th>from</th><th>to</th><th>length</th><th>width</th><th></th></tr></thead>
              <tbody>
                {#each result.steps as s, i}
                  <tr>
                    <td>{i + 1}</td><td>{s.x_start_mm}</td><td>{s.x_end_mm}</td>
                    <td><b>{s.length_mm}</b></td><td><b>{s.width_mm}</b></td>
                    <td class="muted">{s.centred ? '' : 'off axis'}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
            {#if result.round_end}
              <p class="round">
                Round end: &#8960; {result.round_end.diameter_mm} mm, arc centre at x = {result.round_end.centre_x_mm} mm
                <span class="muted">(fit {result.round_end.fit_rms_mm} mm)</span>
              </p>
            {:else}
              <p class="muted">The leading end is not a clean arc (best circle fit was {result.tip_fit_rms_mm} mm out).</p>
            {/if}

            <div class="scad">
              <div class="scad-top"><span>For main.scad</span><button class="link" onclick={copy}>{copied ? 'Copied' : 'Copy'}</button></div>
              <pre>{result.openscad}</pre>
            </div>

            <ul class="notes">
              {#each result.notes ?? [] as n}<li>{n}</li>{/each}
            </ul>
          </div>
        {/if}
      </div>
    </div>
  {/if}
</dialog>

<style>
  dialog { width: min(1100px, 94vw); height: min(780px, 92vh); padding: 0; border: 1px solid var(--line); border-radius: 10px; background: var(--card); color: var(--text); }
  dialog::backdrop { background: #0008; }
  header { display: flex; gap: 10px; align-items: center; padding: 10px 14px; border-bottom: 1px solid var(--line); }
  .body { display: grid; grid-template-columns: minmax(0, 1fr) 340px; height: calc(100% - 46px); }
  .picture { display: flex; flex-direction: column; min-width: 0; padding: 10px; gap: 6px; background: var(--bg); }
  .picture img { flex: 1; min-height: 0; width: 100%; object-fit: contain; }
  .legend { margin: 0; font-size: 12px; color: var(--muted); display: flex; gap: 6px; align-items: center; flex-wrap: wrap; }
  .key { width: 12px; height: 3px; border-radius: 2px; display: inline-block; margin-left: 8px; }
  .key.outline { background: #e62828; } .key.step { background: #28bee6; } .key.tip { background: #ffa514; }
  .side-panel { overflow: auto; padding: 12px; display: flex; flex-direction: column; gap: 10px; border-left: 1px solid var(--line); font-size: 13px; }
  fieldset { border: 1px solid var(--line); border-radius: 8px; margin: 0; padding: 8px 10px; }
  legend { padding: 0 4px; color: var(--muted); font-size: 12px; }
  .radio, .check { display: flex; gap: 6px; align-items: center; padding: 2px 0; }
  .num { display: flex; gap: 6px; align-items: center; margin-top: 6px; }
  .num input { width: 90px; }
  .hint { margin: 6px 0 0; color: var(--muted); font-size: 12px; }
  .field { display: flex; justify-content: space-between; gap: 8px; align-items: center; padding: 3px 0; }
  .field input { width: 90px; }
  input[type="number"], select { font: inherit; font-size: 13px; padding: 3px 6px; border-radius: 6px; background: var(--card); color: var(--text); border: 1px solid var(--line); }
  details summary { cursor: pointer; color: var(--muted); font-size: 12px; }
  .overall { margin: 0; display: flex; flex-direction: column; }
  table { width: 100%; border-collapse: collapse; font: 12px var(--mono); }
  th { text-align: right; color: var(--faint); font-weight: 400; }
  td { text-align: right; padding: 1px 0; }
  th:first-child, td:first-child, th:last-child, td:last-child { text-align: left; }
  .round { margin: 0; }
  .scad { border: 1px solid var(--line); border-radius: 8px; overflow: hidden; }
  .scad-top { display: flex; justify-content: space-between; padding: 4px 8px; background: var(--bg); color: var(--muted); font-size: 12px; }
  .scad pre { margin: 0; padding: 8px; font: 12px var(--mono); overflow: auto; white-space: pre-wrap; }
  .link { background: none; border: 0; padding: 0; color: var(--accent); font-size: 12px; cursor: pointer; }
  .notes { margin: 0; padding-left: 16px; color: var(--muted); font-size: 12px; }
  .notes li { margin: 3px 0; }
  .error { color: var(--err); margin: 0; }
  @media (max-width: 880px) {
    .body { grid-template-columns: minmax(0, 1fr); height: auto; }
    .side-panel { border-left: 0; border-top: 1px solid var(--line); }
  }
</style>
