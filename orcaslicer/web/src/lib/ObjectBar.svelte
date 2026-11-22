<script>
  // Inputs and buttons for the selected object. Every change is one PATCH; the parent applies the plate it returns.
  let { object = null, model = null, onpatch, onduplicate, ondelete } = $props()

  const wrap = (v) => ((v % 360) + 360) % 360
  const num = (e) => {
    const v = e.currentTarget.valueAsNumber
    return Number.isFinite(v) ? v : null
  }

  function setX(e) {
    const v = num(e)
    if (v !== null) onpatch(object.id, { x: v })
  }
  function setY(e) {
    const v = num(e)
    if (v !== null) onpatch(object.id, { y: v })
  }
  function setScale(e) {
    const v = num(e)
    if (v !== null && v > 0) onpatch(object.id, { scale: v })
  }
  function setRot(axis, e) {
    const v = num(e)
    if (v === null) return
    const r = [...object.rotation]
    r[axis] = wrap(v)
    onpatch(object.id, { rotation: r })
  }
  function turn(axis, delta) {
    const r = [...object.rotation]
    r[axis] = wrap(r[axis] + delta)
    onpatch(object.id, { rotation: r })
  }
  const axes = ['X', 'Y', 'Z']
</script>

<div class="bar">
  {#if object}
    <div class="name" title={model?.name ?? object.name}>{object.name}</div>
    <div class="fields">
      <label>X <input type="number" step="0.5" value={object.x} onchange={setX} /></label>
      <label>Y <input type="number" step="0.5" value={object.y} onchange={setY} /></label>
      {#each axes as a, i (a)}
        <label>Rot {a} <input type="number" step="1" value={object.rotation[i]} onchange={(e) => setRot(i, e)} /></label>
      {/each}
      <label>Scale <input type="number" step="0.05" min="0.01" value={object.scale} onchange={setScale} /></label>
    </div>
    <div class="buttons">
      {#each axes as a, i (a)}
        <button onclick={() => turn(i, 90)} title="Rotate 90° about {a}">{a} +90°</button>
        <button onclick={() => turn(i, -90)} title="Rotate -90° about {a}">{a} −90°</button>
      {/each}
      <span class="sep"></span>
      <button onclick={() => onduplicate(object.id)}>Duplicate</button>
      <button class="danger" onclick={() => ondelete(object.id)}>Delete</button>
    </div>
  {:else}
    <div class="muted">Click an object on the bed to select it, drag it to move. Add models from the library on the left.</div>
  {/if}
</div>

<style>
  .bar {
    display: flex; flex-wrap: wrap; gap: 8px 14px; align-items: center; padding: 8px 12px;
    border-bottom: 1px solid var(--line); min-height: 44px;
  }
  .name { font-weight: 600; max-width: 180px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .fields { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; font-size: 12px; color: var(--muted); }
  .fields label { display: flex; gap: 4px; align-items: center; }
  .fields input { width: 72px; }
  .buttons { display: flex; flex-wrap: wrap; gap: 4px; align-items: center; }
  .buttons button { font-size: 12px; padding: 2px 7px; }
  .sep { width: 4px; }
</style>
