import { useEffect } from "react"

// Sets document.title to "<title> · Darkpanel" (or "Darkpanel" for "") while
// the calling component is mounted, and restores the previous title after.
export function usePageTitle(title: string): void {
  useEffect(() => {
    // Read inside the effect: React runs every cleanup of a commit before any
    // new effect, so a page replacing another captures the restored title.
    const previous = document.title
    document.title = title === "" ? "Darkpanel" : `${title} · Darkpanel`
    return () => {
      document.title = previous
    }
  }, [title])
}
