import { spawn, type ChildProcessWithoutNullStreams } from "node:child_process"
import { fileURLToPath } from "node:url"
import { expect, test } from "@playwright/test"

// A55 (UX-3): OIDC sign-in across sites. TestServeOIDCE2E serves the real
// controller and the built web UI on https://127.0.0.1:<p> and a stub
// provider on https://localhost:<q>, another site, so the Lax state cookie
// and the Strict session cookie meet a real cross-site redirect.

const repo = fileURLToPath(new URL("../../", import.meta.url))

test.use({ ignoreHTTPSErrors: true })

let server: ChildProcessWithoutNullStreams | null = null
let app = ""
let idp = ""

test.beforeAll(async () => {
  // go test compiles the API package first.
  test.setTimeout(600_000)
  const child = spawn("go", ["test", "./internal/controller/api", "-run", "^TestServeOIDCE2E$", "-count=1", "-v"], {
    cwd: repo,
    env: { ...process.env, DARKPANEL_E2E_SERVE: "1" },
    detached: true,
  })
  server = child
  const ready = await new Promise<RegExpExecArray>((resolve, reject) => {
    let out = ""
    const read = (chunk: Buffer) => {
      out += chunk.toString()
      const m = /E2E_READY app=(\S+) idp=(\S+)/.exec(out)
      if (m !== null) resolve(m)
    }
    child.stdout.on("data", read)
    child.stderr.on("data", read)
    child.on("exit", (code) => reject(new Error(`the e2e server exited with ${code}: ${out.slice(-4000)}`)))
  })
  app = ready[1] ?? ""
  idp = ready[2] ?? ""
})

test.afterAll(() => {
  // The process group holds go test and the test binary it runs.
  if (server?.pid !== undefined) {
    try {
      process.kill(-server.pid, "SIGTERM")
    } catch {
      // already gone
    }
  }
})

test("completes in Chromium", async ({ page, context }) => {
  expect(new URL(idp).hostname).toBe("localhost")
  expect(new URL(app).hostname).toBe("127.0.0.1")
  await page.goto(`${app}/login`)
  await page.getByRole("link", { name: "Sign in with Stub" }).click()
  await expect(page).toHaveURL(new RegExp(`^${idp}/authorize`))
  await page.getByRole("link", { name: "Continue to Darkpanel" }).click()
  await expect(page.getByRole("heading", { level: 1, name: "Hosts" })).toBeVisible()
  expect(new URL(page.url()).pathname).toBe("/")
  expect(new URL(page.url()).search).toBe("")
  const session = (await context.cookies(app)).find((c) => c.name === "__Host-darkpanel_session")
  expect(session?.sameSite).toBe("Strict")
  expect(session?.secure).toBe(true)
  expect(session?.httpOnly).toBe(true)
})
