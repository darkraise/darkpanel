import type { ThemeConfig } from "darkraise-ui/theme"

export const themeConfig: ThemeConfig = {
  defaults: {
    accentColor: "blue",
    surfaceColor: "zinc",
    style: "quiet",
    backgroundStyle: "solid",
    backgroundIntensity: "balanced",
    gradientPattern: "blobs",
    mode: "dark",
    density: "compact",
    radius: "rounded",
    fontSize: "medium",
    outerGlow: "none",
    innerGlow: "none",
  },
  switcher: {
    enabled: true,
    axes: {
      mode: true,
      accentColor: true,
      surfaceColor: true,
      style: true,
      preset: true,
      backgroundStyle: true,
      backgroundIntensity: true,
      gradientPattern: true,
      density: true,
      radius: true,
      fontSize: true,
      outerGlow: true,
      innerGlow: true,
    },
  },
}
