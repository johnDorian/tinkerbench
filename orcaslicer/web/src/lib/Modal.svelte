<script>
  // Plain modal: backdrop click and Escape close it. Full width on phones.
  let { title = '', onclose, children } = $props()
  const onkey = (e) => {
    if (e.key === 'Escape') onclose?.()
  }
</script>

<svelte:window onkeydown={onkey} />

<div class="backdrop" onclick={(e) => e.target === e.currentTarget && onclose?.()} role="presentation">
  <div class="dialog panel" role="dialog" aria-modal="true" aria-label={title}>
    <header class="panel-bar">
      <span class="panel-title">{title}</span>
      <button class="ghost close" onclick={onclose} aria-label="Close">×</button>
    </header>
    <div class="body">{@render children?.()}</div>
  </div>
</div>

<style>
  .backdrop {
    position: fixed; inset: 0; background: rgb(0 0 0 / 0.35); display: grid; place-items: center;
    padding: 12px; z-index: 50;
  }
  .dialog { width: min(520px, 100%); max-height: calc(100dvh - 24px); }
  .close { font-size: 18px; line-height: 1; padding: 0 4px; margin-left: auto; }
  .body { padding: 14px; overflow: auto; }
</style>
