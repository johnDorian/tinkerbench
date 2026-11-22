<script>
  // Scans and photos of the real part, in the side panel. Drop a file here, then measure it.
  import { api, ago } from './api.js'

  /** images: [{name, size, width, height, dpi, modified}] */
  let { project, images = [], onchanged, onmeasure } = $props()

  let dragging = $state(false)
  let busy = $state(false)
  let error = $state('')
  let picker

  async function send(files) {
    const list = [...files].filter((f) => /\.(png|jpe?g)$/i.test(f.name))
    if (!list.length) {
      error = 'Only PNG and JPEG files can be measured.'
      return
    }
    busy = true
    error = ''
    try {
      await api.uploadImages(project, list)
      onchanged?.()
    } catch (e) {
      error = e.message
    } finally {
      busy = false
    }
  }

  function onDrop(e) {
    e.preventDefault()
    dragging = false
    send(e.dataTransfer.files)
  }

  async function remove(img) {
    if (!confirm(`Delete ${img.name}? This cannot be undone.`)) return
    try {
      await api.deleteImage(project, img.name)
      onchanged?.()
    } catch (e) {
      error = e.message
    }
  }

  const kb = (n) => (n > 1 << 20 ? `${(n / (1 << 20)).toFixed(1)} MB` : `${Math.round(n / 1024)} kB`)
</script>

<div
  class="images"
  class:dragging
  ondragover={(e) => { e.preventDefault(); dragging = true }}
  ondragleave={() => (dragging = false)}
  ondrop={onDrop}
  role="region"
  aria-label="Images of the part"
>
  <div class="top">
    <button onclick={() => picker.click()} disabled={busy}>{busy ? 'Uploading…' : 'Add image'}</button>
    <input bind:this={picker} type="file" accept="image/png,image/jpeg" multiple hidden
           onchange={(e) => { send(e.currentTarget.files); e.currentTarget.value = '' }} />
  </div>

  {#if error}<p class="error">{error}</p>{/if}

  {#each images as img (img.name)}
    <figure>
      <button class="thumb" onclick={() => onmeasure(img)} title="Measure {img.name}">
        <img src={api.imageUrl(project, img.name, 240)} alt={img.name} loading="lazy" />
      </button>
      <figcaption>
        <span class="name" title={img.name}>{img.name}</span>
        <span class="meta">
          {img.width}&times;{img.height}
          {#if img.camera}
            · {img.camera}{#if img.dpi} · <span class="dpi-note" title="A phone camera writes a default dpi; it says nothing about the scale">{Math.round(img.dpi)} dpi (camera default, not a scale)</span>{/if}
          {:else if img.dpi && img.dpi_usable}
            · {Math.round(img.dpi)} dpi
          {:else if img.dpi}
            · <span class="dpi-note">{Math.round(img.dpi)} dpi (not a scale)</span>
          {:else}
            · no dpi
          {/if}
          · {kb(img.size)} · {ago(img.modified)}
        </span>
        <span class="row">
          <button class="link" onclick={() => onmeasure(img)}>Measure</button>
          <a class="link" href={api.imageUrl(project, img.name) + '?download'} download>Download</a>
          <button class="link danger" onclick={() => remove(img)}>Delete</button>
        </span>
      </figcaption>
    </figure>
  {:else}
    <p class="empty">
      Drop a scan or a photo here to copy a real part. A flatbed scan at 600–1200 dpi is best:
      it carries its own scale, so nothing has to be calibrated.
    </p>
  {/each}
</div>

<style>
  .images { overflow: auto; padding: 8px; display: flex; flex-direction: column; gap: 10px; font-size: 13px; }
  .images.dragging { outline: 2px dashed var(--accent); outline-offset: -4px; background: var(--accent-soft); }
  .top { display: flex; gap: 6px; }
  figure { margin: 0; display: flex; flex-direction: column; gap: 4px; }
  .thumb { padding: 0; border: 1px solid var(--line); border-radius: 6px; overflow: hidden; background: var(--bg); cursor: pointer; }
  .thumb img { display: block; width: 100%; height: auto; max-height: 160px; object-fit: contain; }
  .thumb:hover { border-color: var(--accent); }
  figcaption { display: flex; flex-direction: column; gap: 2px; }
  .name { font: 12px var(--mono); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .meta { color: var(--muted); font-size: 11px; }
  .dpi-note { color: var(--faint); }
  .row { display: flex; gap: 8px; }
  .link { background: none; border: 0; padding: 0; color: var(--accent); font-size: 12px; cursor: pointer; text-decoration: none; }
  .link.danger { color: var(--err); }
  .empty, .error { color: var(--muted); margin: 4px 0; }
  .error { color: var(--err); }
</style>
