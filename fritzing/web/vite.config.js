import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// `npm run dev` serves the UI on :5173 and proxies the API to the Go server (task fritzing:run).
export default defineConfig({
  plugins: [svelte()],
  build: { emptyOutDir: false }, // dist/.gitkeep must survive builds so `go build` works before the first one
  server: { proxy: { '/api': 'http://localhost:8030', '/mcp': 'http://localhost:8030' } },
})
