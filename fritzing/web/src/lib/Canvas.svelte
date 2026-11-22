<script>
  // Shows one rendered SVG with wheel zoom and drag pan; resets when `reset` changes.
  let { src, reset = '' } = $props()
  let scale = $state(1), x = $state(0), y = $state(0), drag = null
  let box
  $effect(() => { reset; scale = 1; x = 0; y = 0 })

  function wheel(e) {
    e.preventDefault()
    const r = box.getBoundingClientRect()
    const k = Math.exp(-e.deltaY * 0.0015)
    const px = e.clientX - r.left, py = e.clientY - r.top
    x = px - (px - x) * k
    y = py - (py - y) * k
    scale = Math.min(Math.max(scale * k, 0.2), 20)
  }
  function down(e) { drag = { sx: e.clientX - x, sy: e.clientY - y }; box.setPointerCapture(e.pointerId) }
  function move(e) { if (drag) { x = e.clientX - drag.sx; y = e.clientY - drag.sy } }
  function up() { drag = null }
</script>

<div class="box" bind:this={box} onwheel={wheel} onpointerdown={down} onpointermove={move} onpointerup={up} ondblclick={() => { scale = 1; x = 0; y = 0 }} role="img">
  {#if src}
    <img {src} alt="sketch view" draggable="false" style="transform: translate({x}px, {y}px) scale({scale})" />
  {/if}
</div>

<style>
  .box { position: relative; flex: 1; min-height: 0; overflow: hidden; background: #fff; cursor: grab; touch-action: none; }
  .box:active { cursor: grabbing; }
  img { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: contain; transform-origin: 0 0; user-select: none; }
</style>
