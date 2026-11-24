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

// Below the sm breakpoint a table lays out as stacked cards: its header row
// is kept for screen readers only, each row is a bordered card, and each cell
// a line that shows its column's name from its data-label first.
export const STACKED_TABLE = "max-sm:block"
export const STACKED_HEAD = "max-sm:sr-only"
export const STACKED_BODY = "max-sm:flex max-sm:flex-col max-sm:gap-3"
export const STACKED_ROW = "max-sm:flex max-sm:flex-col max-sm:gap-1 max-sm:rounded-lg max-sm:border max-sm:border-border max-sm:p-3"
export const STACKED_CELL =
  "max-sm:flex max-sm:max-w-none max-sm:items-center max-sm:whitespace-normal max-sm:text-right max-sm:wrap-anywhere max-sm:justify-between max-sm:gap-3 max-sm:p-0 max-sm:text-left max-sm:before:shrink-0 max-sm:before:text-xs max-sm:before:text-muted-foreground max-sm:before:content-[attr(data-label)]"
