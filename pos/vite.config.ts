import { fileURLToPath } from 'node:url';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Tauri serves the built assets from disk, so the base is relative.
export default defineConfig({
  resolve: {
    // The shared package is consumed from source, not from a build step. One
    // less artefact to keep in step, and the POS and the back-office cannot
    // drift onto different versions of the same component.
    alias: { '@rawsyst/shared': fileURLToPath(new URL('../shared/src', import.meta.url)) },
  },
  plugins: [react()],
  base: './',
  build: { outDir: 'dist', target: 'es2022' },
  server: { port: 5173, strictPort: true },
});
