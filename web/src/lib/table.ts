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
