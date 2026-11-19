# ADR 0017: Design language

**Requirements:** none

## Status

Proposed

## Context

Darkpanel's screens were built on darkraise-ui 6.x, mostly as key/value text
blocks and long explanatory sentences. The theme switcher let each user change
the style, preset, surface colour, density, radius and font size, so no
contrast or layout check covered the look users actually saw. Version 7 of the
kit adds the Glass style, presets, status token pairs and a colour engine that
solves status colours for contrast. The UI revamp and the new server-management
screens need one standard to build to.

## Decision

- **A fixed look.** The app uses the Glass style with a `darkpanel` preset
  (the kit's Mica Desk preset with a violet signal colour, Hanken Grotesk,
  compact density and boxed fields). The switcher offers only mode and accent
  colour. `web/public/theme-init.js` paints the look before React mounts, and
  once per browser it removes every other stored theme choice.
- **A visual vocabulary.** Screens show state with shared components in
  `web/src/components/visual/`: status dots and pills, gauges, usage bars,
  stat tiles, status donuts, resource cards, a stepper, info tips, section
  headers and module icons. Every graphic carries its value as text.
- **A copy budget.** No visible text node is longer than 90 characters.
  Longer explanation goes in an info tip or a dialog body.
- **Guards.** Two Vitest guards fail the build on a colour literal outside
  `web/src/theme.config.ts`, and on visible text over 90 characters outside
  info tips, dialog bodies and a commented allowlist.
- **The standard.** `docs/dev/design-language.md` states the rules, and
  `CONTRIBUTING.md` links it.

## Alternatives considered

- Stay on darkraise-ui 6.x: no status token pairs or colour engine, and the
  kit's later fixes do not reach the app.
- Upgrade to 7.x and keep the free switcher: every combination of style,
  preset, density and radius would need its own contrast and layout proof,
  and screenshots in the documentation would not match what users see.

## Consequences

- Users lose the stored style, preset, density, radius and font-size choices
  they made under 6.x. Their mode and accent colour are kept.
- New screens reuse the visual vocabulary rather than adding one-off
  components. A new kind of graphic is added to the vocabulary and the
  standard first.
- Text over the budget needs an allowlist entry with a reason, so long copy
  shows up in review.
