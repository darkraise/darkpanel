import { defineConfig } from "vite"
import react from "@vitejs/plugin-react-swc"
import tailwindcss from "@tailwindcss/vite"
import path from "node:path"

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(import.meta.dirname, "./src"),
    },
  },
  build: {
    // The CSP allows fonts only from 'self', so no asset may be inlined as a
    // data: URI.
    assetsInlineLimit: 0,
  },
  server: {
    host: "localhost",
    port: 5173,
  },
})
