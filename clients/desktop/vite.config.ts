import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { version as desktopVersion } from './src-tauri/tauri.conf.json';

export default defineConfig({
  plugins: [react()],
  define: {
    'import.meta.env.VITE_DESKTOP_VERSION': JSON.stringify(process.env.VITE_DESKTOP_VERSION || desktopVersion),
  },
  // Direct engine connection; Tauri's devUrl requires the fixed Vite port.
  server: { host: '127.0.0.1', port: 5173, strictPort: true },
  preview: { host: '127.0.0.1' },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    globals: true,
    css: false,
  },
});
