import { cleanup } from "@testing-library/react"
import { afterEach, vi } from "vitest"
import { setCsrfToken, setUnauthorizedHandler } from "@/api/client"

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

// Testing Library cleans up on its own only when afterEach is a global, and
// this suite does not enable Vitest's globals.
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  localStorage.clear()
  setCsrfToken(null)
  setUnauthorizedHandler(null)
})
