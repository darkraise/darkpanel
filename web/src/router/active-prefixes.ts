// The kit marks its sidebar links activeExact, so the pages that belong to a
// link but sit outside its path are listed here by the link's `to`.
const ACTIVE_PREFIXES: Readonly<Record<string, readonly string[]>> = {
  "/": ["/hosts/"],
}

export function activeByPrefix(to: string, pathname: string): boolean {
  return (ACTIVE_PREFIXES[to] ?? []).some((prefix) => pathname.startsWith(prefix))
}
