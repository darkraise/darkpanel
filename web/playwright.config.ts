import { defineConfig, devices } from "@playwright/test"

export default defineConfig({
  testDir: "e2e",
  testMatch: "*.pw.ts",
  outputDir: "node_modules/.playwright-results",
  workers: 1,
  retries: 0,
  timeout: 60_000,
  reporter: "list",
  projects: [
    {
      name: "chromium",
      use: {
        ...devices["Desktop Chrome"],
        // A Chromium of another cached build may stand in when the build this
        // Playwright pins is not installed.
        launchOptions: { executablePath: process.env.E2E_CHROMIUM || undefined },
      },
    },
  ],
  // Vite picks a free port itself; the tests read it from E2E_PORT, which
  // Playwright sets from the named group of the server's first output line.
  webServer: {
    command: "pnpm exec vite --host 127.0.0.1 --port 0 --strictPort false",
    wait: { stdout: /Local:\s+http:\/\/127\.0\.0\.1:(?<e2e_port>\d+)/ },
    timeout: 120_000,
    reuseExistingServer: false,
  },
})
