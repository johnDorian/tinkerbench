<script>
  // Plain modal: backdrop click and Escape close it. Full width on phones.
  let { title = '', onclose, children } = $props()
  const onkey = (e) => {
    if (e.key === 'Escape') onclose?.()
  }
</script>

<svelte:window onkeydown={onkey} />

<div class="backdrop" onclick={(e) => e.target === e.currentTarget && onclose?.()} role="presentation">
  <div class="dialog card" role="dialog" aria-modal="true" aria-label={title}>
    <header>
      <b>{title}</b>
      <button class="close" onclick={onclose} aria-label="Close">×</button>
    </header>
    <div class="body">{@render children?.()}</div>
  </div>
</div>

<style>
  .backdrop {
    position: fixed; inset: 0; background: rgb(0 0 0 / 0.35); display: grid; place-items: center;
    padding: 12px; z-index: 50;
  }
  .dialog { width: min(520px, 100%); max-height: calc(100dvh - 24px); display: flex; flex-direction: column; }
  header {
    display: flex; justify-content: space-between; align-items: center; padding: 10px 14px;
    border-bottom: 1px solid var(--line);
  }
  .close { background: none; border: 0; font-size: 18px; line-height: 1; padding: 0 4px; }
  .body { padding: 14px; overflow: auto; }
</style>
