import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';

// extApps puts the MCP Apps library in the build: the second origin serves it to custom blocks as /ext-apps.js.
const extApps = (): Plugin => ({
  name: 'deck-ext-apps',
  generateBundle() {
    const file = createRequire(import.meta.url).resolve('@modelcontextprotocol/ext-apps/app-with-deps');
    this.emitFile({ type: 'asset', fileName: 'ext-apps.js', source: readFileSync(file) });
  },
});

export default defineConfig({
  plugins: [react(), extApps()],
  base: '/',
  build: { outDir: 'dist', emptyOutDir: true, chunkSizeWarningLimit: 1500 },
});
