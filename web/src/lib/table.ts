// darkraise-ui's Table scrolls inside its own wrapper, which takes no props. A
// table whose rows hold only text leaves nothing in that scroller for Tab to
// reach, so a keyboard user could not scroll it: this ref callback makes the
// wrapper a named, focusable region.
export function focusableScroller(label: string) {
  return (table: HTMLTableElement | null) => {
    const wrapper = table?.parentElement
    if (wrapper === null || wrapper === undefined) return
    wrapper.setAttribute("tabindex", "0")
    wrapper.setAttribute("role", "region")
    wrapper.setAttribute("aria-label", label)
  }
}

// When its frame is narrower than 48rem a table lays out as stacked cards:
// its header row is kept for screen readers only, each row is a bordered card,
// and each cell a line that shows its column's name from its data-label first.
// The frame, not the viewport, decides, because the sidebar takes a third of a
// 768 px screen. The kit gives every cell a fixed height and no wrapping,
// which a card's lines must undo.
export const STACKED_FRAME = "@container"
export const STACKED_TABLE = "@max-3xl:block"
export const STACKED_HEAD = "@max-3xl:sr-only"
export const STACKED_BODY = "@max-3xl:flex @max-3xl:flex-col @max-3xl:gap-3"
export const STACKED_ROW = "@max-3xl:flex @max-3xl:flex-col @max-3xl:gap-1 @max-3xl:rounded-lg @max-3xl:border @max-3xl:border-border @max-3xl:p-3"
export const STACKED_CELL =
  "@max-3xl:flex @max-3xl:max-w-none @max-3xl:items-center @max-3xl:whitespace-normal @max-3xl:text-right @max-3xl:wrap-anywhere @max-3xl:justify-between @max-3xl:gap-3 @max-3xl:h-auto @max-3xl:overflow-visible @max-3xl:p-0 @max-3xl:text-left @max-3xl:before:shrink-0 @max-3xl:before:text-xs @max-3xl:before:text-muted-foreground @max-3xl:before:content-[attr(data-label)]"
