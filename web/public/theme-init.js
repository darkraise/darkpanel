// Restores the persisted theme before React mounts, so the first paint
// matches it. Loaded with a plain <script src> because the CSP allows no
// inline script. With nothing stored it falls back to dark, the default in
// src/theme.config.ts.
;(function () {
  const mode = localStorage.getItem("mode") || "dark"
  const resolved =
    mode === "system"
      ? window.matchMedia("(prefers-color-scheme: dark)").matches
        ? "dark"
        : "light"
      : mode
  document.documentElement.setAttribute("data-mode", resolved)

  // Restore every attribute-driven theme axis. The attribute-name to
  // localStorage-key map mirrors darkraise-ui's ThemeProvider — note
  // "theme-bg-style" / "theme-bg-intensity" are NOT just kebab-cased
  // "background-*" keys.
  const AXIS_LS_KEYS = {
    preset: "theme-preset",
    "background-style": "theme-bg-style",
    "background-intensity": "theme-bg-intensity",
    "gradient-pattern": "theme-gradient-pattern",
    density: "theme-density",
    elevation: "theme-elevation",
    "button-elevation": "theme-button-elevation",
    "surface-intensity": "theme-surface-intensity",
    radius: "theme-radius",
    "control-depth": "theme-control-depth",
    "shell-style": "theme-shell-style",
    "sidebar-active-bar": "theme-sidebar-active-bar",
    "font-size": "theme-font-size",
    "outer-glow": "theme-outer-glow",
    "inner-glow": "theme-inner-glow",
  }
  // Presets that reinterpret an axis neutralise it rather than merely hiding
  // its control, so restoring a stored value here would flash the wrong
  // surface treatment before hydration corrects it. Mirrors
  // NEUTRALISED_WHEN_HIDDEN + hiddenCommonAxes in the library.
  const NEUTRALISED_BY_PRESET = {
    glass: ["surface-intensity"],
    scifi: ["surface-intensity"],
  }
  const activePreset = localStorage.getItem("theme-preset") || ""
  const neutralised = NEUTRALISED_BY_PRESET[activePreset] || []
  Object.keys(AXIS_LS_KEYS).forEach(function (axis) {
    if (neutralised.indexOf(axis) !== -1) return
    const v = localStorage.getItem(AXIS_LS_KEYS[axis])
    if (v) document.documentElement.setAttribute("data-" + axis, v)
  })
})()
