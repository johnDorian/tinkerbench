import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// `npm run dev` serves the UI on :5173 and proxies the API to the Go server (task openscad:run).
export default defineConfig({
  plugins: [svelte()],
  build: {
    // dist/.gitkeep must survive builds so `go build` works before the first frontend build
    emptyOutDir: false,
    chunkSizeWarningLimit: 1000, // three.js is most of the bundle
  },
  server: {
    proxy: {
      '/api': 'http://localhost:8020',
      '/mcp': 'http://localhost:8020',
    },
  },
})
