# Design language

The standard every Darkpanel screen follows. [ADR 0017](../adr/0017-design-language.md)
records why the look is fixed and how it is enforced.

## Principles

- Show state; do not describe it.
- A number with a limit is a gauge or a bar.
- A state is a dot or icon with a short label, never colour alone.
- A count is a figure.
- A list of resources is a card grid, or a table with icons.
- Prose appears only where no picture fits, and then behind an info tip.

## Copy budget

- Write labels, not sentences.
- No single text node in a page or panel is longer than 90 characters. A longer
  explanation goes in an `InfoTip` or a dialog body.
- Buttons are verbs in sentence case: "Install Docker", not "Docker
  installation".
- Text that arrives at run time, such as a backend reason or an error message,
  renders through an `InfoTip` or an alert's detail line, because no static
  check can measure it.

## Visual vocabulary

Shared components live in `web/src/components/visual/`, one file per component,
each a named export. `status` is always `"ok" | "warn" | "error" | "info" |
"off"`. Each status has its own lucide icon: `CircleCheck`, `TriangleAlert`,
`CircleX`, `Info` and `CircleMinus`.

| Component | Purpose | Props |
|---|---|---|
| `StatusDot` | A state as an icon, named for screen readers | `status`, `label` |
| `StatusPill` | A state as an icon and a visible label; `live` announces changes | `status`, `label`, `icon?`, `live?` |
| `StatTile` | A summary figure with an icon and caption | `icon`, `figure`, `caption`, `children?` |
| `Gauge` | A radial view of a value against its limit | `value`, `max`, `label`, `unit?` |
| `UsageBar` | A segmented bar showing used of total, with the figures | `used`, `total`, `label`, `unit?` |
| `StatusDonut` | The share of each state, such as Docker containers or rollout outcomes | `segments: {status, label, value}[]`, `label` |
| `ResourceCard` | One resource: icon tile, title, status, figures, actions | `icon`, `title`, `status`, `figures?`, `actions?`, `children?` |
| `Stepper` | The progress of a change: `ChangeDialog`, the change page, reboot and Docker install | `steps: {id, label}[]`, `current`, `failed?` |
| `InfoTip` | Explanation behind an info button; opens on hover, keyboard focus or tap | `label`, `children` |
| `SectionHeader` | A section title (`h2`) with an optional icon, status and actions | `icon?`, `title`, `status?`, `actions?` |
| `ModuleIcon` | The icon of a module or resource kind; `dimmed` marks an unavailable module | `module`, `dimmed?` |

Figures (counts, sizes, percentages, versions) are set in the tabular
monospace face, so columns of numbers line up.

## Layout

- Each page starts with a header carrying an icon, the title and the page's
  status. A row of tiles summarises the page, and detail follows below.
- Tiles flow on a 12-column grid: 4 per row at 1440 px, 2 at 768 px and 1 at
  375 px.
- No page scrolls horizontally at 375 × 812.

## Colour and tokens

- Use kit tokens only. No colour literal (`#hex`, `rgb(`, `hsl(`, `oklch(`)
  appears in `web/src` outside `web/src/theme.config.ts`.
- Status uses the status token pairs, such as `text-success-text` and
  `bg-warning-subtle`. The accent is reserved for primary actions and
  selection.
- The look is fixed: style `glass`, preset `darkpanel`, density `compact`.
  The theme switcher offers only mode (default dark) and accent colour.

### The darkpanel preset

`darkpanel` is the kit's Glass "Mica Desk" preset (darkraise-ui 7.2.2), defined
in `web/src/theme.config.ts`, with four changes:

| Setting | Mica Desk | darkpanel | Why |
|---|---|---|---|
| `signalColor` | default | `violet` | marks that must not read as the blue accent stay distinct from the info status |
| `font` | Inter | `hanken-grotesk` | Darkpanel's face, shipped by the kit |
| `density` | default | `compact` | an operations panel shows more rows per screen |
| `fieldBorder` | `underline` | `box` | the underline left a field's boundary at 1.00:1, under the 3:1 that WCAG 1.4.11 asks for |

All other axes, signatures and tokens are Mica Desk's: blue accent, zinc
surfaces, no accent tint, `subtle` radius, `inset` shell, pill navigation
selection and underline tabs. Status colours come from the kit's colour engine,
which solves their lightness for contrast in both modes.

`web/public/theme-init.js` paints this look before React mounts. Once per
browser (marker key `darkpanel-theme-v2`) it removes every stored `theme-*` key
except `theme-accent`.

## States

- **Loading:** a skeleton shaped like the content.
- **Empty:** an icon, one line and one action.
- **Error:** an inline alert with a retry.

## Accessibility

- Every graphic carries its value as text: `role="img"` with an `aria-label`
  such as "Memory: 6.2 of 8 GiB (78%)", or the kit's `ChartContainer
  description`.
- A status change announces through a live region (`StatusPill live`).
- Text keeps 4.5:1 contrast and graphics 3:1 on the glass surfaces, in both
  modes.
- Every flow works by keyboard alone.

## Enforcement

Two Vitest guards in `web/src/test/design-guards.test.ts` enforce the rules:

- **Colour:** no colour literal in `web/src/**/*.{ts,tsx,css}` except
  `theme.config.ts`.
- **Copy:** no JSX text, and no literal string passed to a visible-text prop,
  longer than 90 characters. The checked props are `title`, `description`,
  `label`, and the `children` of `Alert*`, `EmptyState`, `StatTile`,
  `StatusPill`, `SectionHeader`, `CardTitle` and `Badge`. Text inside `InfoTip`
  and dialog bodies is exempt. A template literal is measured by its static
  parts.

A text that must exceed the budget goes in `web/src/test/copy-allowlist.ts`,
with a reason for each entry.
