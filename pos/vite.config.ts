import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// Tauri serves the built assets from disk, so the base is relative.
export default defineConfig({
  plugins: [react()],
  base: './',
  build: { outDir: 'dist', target: 'es2022' },
  server: { port: 5173, strictPort: true },
});
