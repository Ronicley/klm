import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  // Direct engine connection; Tauri's devUrl requires the fixed Vite port.
  server: { host: '127.0.0.1', port: 17333, strictPort: true },
  preview: { host: '127.0.0.1' },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test-setup.ts'],
    globals: true,
    css: false,
  },
});
