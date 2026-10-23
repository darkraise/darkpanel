import { cleanup } from "@testing-library/react"
import { afterEach, vi } from "vitest"
import { setCsrfRejectedHandler, setCsrfToken, setUnauthorizedHandler } from "@/api/client"
import { EvtSource } from "./evt-source"

// jsdom has no matchMedia, and the darkraise-ui theme provider reads it on mount.
Object.defineProperty(window, "matchMedia", {
  writable: true,
  value: (query: string): MediaQueryList => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
})

// jsdom has no EventSource, and the signed-in shell opens one. It is not a
// stubbed global: afterEach unstubs those.
Object.defineProperty(globalThis, "EventSource", { writable: true, configurable: true, value: EvtSource })

// Testing Library cleans up on its own only when afterEach is a global, and
// this suite does not enable Vitest's globals.
afterEach(() => {
  cleanup()
  EvtSource.instances = []
  vi.unstubAllGlobals()
  localStorage.clear()
  setCsrfToken(null)
  setUnauthorizedHandler(null)
  setCsrfRejectedHandler(null)
})
