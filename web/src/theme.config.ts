import type { AxisKey, ThemeConfig } from "darkraise-ui/theme"

// The design language fixes every axis but mode and accent colour.
const switcherAxes = {
  mode: true,
  accentColor: true,
  surfaceColor: false,
  tintWithAccent: false,
  signalColor: false,
  style: false,
  preset: false,
  backgroundStyle: false,
  gradientPattern: false,
  backgroundIntensity: false,
  glassTransparency: false,
  glassBlur: false,
  font: false,
  headingFont: false,
  radius: false,
  density: false,
  fontSize: false,
  contrast: false,
  darkAccentChroma: false,
  elevation: false,
  outerGlow: false,
  innerGlow: false,
  sidebarSelection: false,
} satisfies Record<AxisKey, boolean>

// darkpanel is the kit's Glass "Mica Desk" preset (7.2.3) with four changes:
// a violet signal colour, so marks that must not read as the blue accent are
// not mistaken for the info status; the kit's Hanken Grotesk body face in place
// of Inter; compact density; and boxed fields, because Mica Desk's underline
// leaves a field's boundary under the 3:1 that WCAG 1.4.11 asks for. Status colours come from the kit's colour
// engine, which solves their lightness for contrast in both modes.
export const themeConfig: ThemeConfig = {
  defaults: {
    style: "glass",
    preset: "darkpanel",
    mode: "dark",
    density: "compact",
    accentColor: "blue",
  },
  switcher: {
    enabled: true,
    axes: switcherAxes,
    advanced: false,
  },
  presets: {
    darkpanel: {
      label: "darkpanel",
      style: "glass",
      shellStyle: "inset",
      axes: {
        accentColor: "blue",
        surfaceColor: "zinc",
        tintWithAccent: "off",
        signalColor: "violet",
        font: "hanken-grotesk",
        headingFont: "body",
        radius: "subtle",
        backgroundIntensity: "balanced",
        density: "compact",
      },
      signatures: {
        navSelection: "pill",
        barEdge: "none",
        menuHighlight: "neutral",
        cardDivider: "hairline",
        badgeStyle: "tint",
        fieldBorder: "box",
        dialogFooter: "band",
        buttonStyle: "solid",
        iconButton: "square",
        switchStyle: "track",
        checkStyle: "box",
        tabStyle: "underline",
      },
      tokens: {
        shared: {
          viewTrackSheen: "initial",
          viewTrackShadow: "initial",
          viewTrackBackdrop: "initial",
          shellContentFill: "hsl(var(--card) / max(0.35, calc(var(--surface-alpha) * 2 - 1)))",
          cardRadius: "calc(var(--radius) * 2)",
          overlayRadius: "calc(var(--radius) * 2)",
          dialogRadius: "calc(var(--radius) * 2)",
          navItemRadius: "var(--radius)",
          menuItemRadius: "var(--radius)",
          checkRadius: "var(--radius)",
          buttonEdge: "inset 0 -1px 0 rgb(0 0 0 / 0.4)",
          buttonShadow: "0 0 transparent",
          buttonNeutralShadow: "0 0 transparent",
          fieldFocusBorder: "hsl(var(--primary))",
          fieldFocusRing: "inset 0 -1px 0 hsl(var(--primary))",
          scrim: "rgb(0 0 0 / 0.3)",
          pageTitleTracking: "0",
        },
        light: {
          selectedViewFill: "rgb(255 255 255 / 0.75)",
          selectedViewShadow: "0 0 0 1px hsl(var(--border-strong))",
          surfaceFillAlpha: "0.6",
          shellGroundGradient: "radial-gradient(80% 70% at 10% 0%, hsl(var(--primary) / 0.34), transparent 60%)",
          shellContentEdge: "rgb(0 0 0 / 0.06)",
          cardEdge: "inset 0 1px 0 rgb(255 255 255 / 0.6)",
          buttonNeutralEdge: "inset 0 -1px 0 rgb(0 0 0 / 0.1)",
          fieldShadow: "0 0 0 1px rgb(0 0 0 / 0.06)",
          fieldFocusFill: "hsl(var(--card))",
          ghostHover: "0.04",
          bannerBorder: "rgb(0 0 0 / 0.06)",
          dropdownShadow: "0 0 2px rgb(0 0 0 / 0.12), 0 8px 16px rgb(0 0 0 / 0.14)",
          modalShadow: "0 0 8px rgb(0 0 0 / 0.12), 0 32px 64px rgb(0 0 0 / 0.14)",
        },
        dark: {
          selectedViewFill: "rgb(255 255 255 / 0.14)",
          selectedViewShadow: "0 0 0 1px color-mix(in srgb, hsl(var(--border-strong)), hsl(var(--foreground)) 25%)",
          surfaceFillAlpha: "0.62",
          shellGroundGradient: "radial-gradient(80% 70% at 10% 0%, hsl(var(--primary) / 0.28), transparent 60%)",
          shellContentEdge: "rgb(0 0 0 / 0.1)",
          cardEdge: "inset 0 1px 0 rgb(255 255 255 / 0.08)",
          buttonNeutralEdge: "0 0 transparent",
          fieldShadow: "0 0 0 1px rgb(255 255 255 / 0.08)",
          fieldFocusFill: "hsl(var(--background) / 0.7)",
          ghostHover: "0.06",
          bannerBorder: "hsl(var(--foreground) / 0.08)",
          dropdownShadow: "0 0 2px rgb(0 0 0 / 0.24), 0 8px 16px rgb(0 0 0 / 0.28)",
          modalShadow: "0 0 8px rgb(0 0 0 / 0.3), 0 32px 64px rgb(0 0 0 / 0.37)",
        },
      },
    },
  },
}

export default themeConfig
