// Paints the fixed darkpanel look and the user's two theme choices before
// React mounts, so the first paint matches. Loaded with a plain <script src>
// because the CSP allows no inline script. Only mode and accent colour are the
// user's to choose; once per browser every other stored theme-* key, 6.x and
// 7.x alike, is dropped so a returning user sees the fixed look.
;(function () {
  const root = document.documentElement

  function read(key) {
    try {
      return localStorage.getItem(key)
    } catch {
      return null
    }
  }

  try {
    if (localStorage.getItem("darkpanel-theme-v2") === null) {
      const stale = []
      for (let i = 0; i < localStorage.length; i++) {
        const key = localStorage.key(i)
        if (key !== null && key.indexOf("theme-") === 0 && key !== "theme-accent") stale.push(key)
      }
      stale.forEach(function (k) {
        localStorage.removeItem(k)
      })
      localStorage.setItem("darkpanel-theme-v2", "1")
    }
  } catch {
    // Storage blocked: the fixed look below still paints.
  }

  const mode = read("mode") || "dark"
  const resolved = mode === "system" ? (window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light") : mode
  root.setAttribute("data-mode", resolved)
  const accent = read("theme-accent")
  if (accent) root.setAttribute("data-accent", accent)
  root.setAttribute("data-style", "glass")
  root.setAttribute("data-preset", "darkpanel")
})()
