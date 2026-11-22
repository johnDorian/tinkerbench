import { fileURLToPath } from 'node:url'
import { defineConfig, searchForWorkspaceRoot } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// `npm run dev` serves the UI on :5173 and proxies the API to the Go server (task orcaslicer:run).
// web-shared/ at the repo root (theme.css); in the Docker build it is copied to /web-shared, the same relative place.
const shared = fileURLToPath(new URL('../../web-shared', import.meta.url))

export default defineConfig({
  plugins: [svelte()],
  resolve: { alias: { $shared: shared } },
  build: {
    // dist/.gitkeep must survive builds so `go build` works before the first frontend build
    emptyOutDir: false,
    chunkSizeWarningLimit: 1000, // three.js is most of the bundle
  },
  server: {
    fs: { allow: [searchForWorkspaceRoot(process.cwd()), shared] },
    proxy: {
      '/api': 'http://localhost:8050',
      '/mcp': 'http://localhost:8050',
    },
  },
})
