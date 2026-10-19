// safeNext keeps a post-login redirect inside the app. Browsers drop tabs
// and newlines from URLs and read a backslash as a slash, so "/\t/host" and
// "/\host" name another origin; those characters are refused outright, and
// what is left must resolve to this origin.
export function safeNext(next: string | null): string {
  // eslint-disable-next-line no-control-regex
  if (next === null || !next.startsWith("/") || /[\u0000-\u001f\u007f\\]/.test(next)) return "/"
  let url: URL
  try {
    url = new URL(next, window.location.origin)
  } catch {
    return "/"
  }
  if (url.origin !== window.location.origin || url.pathname === "/login" || url.pathname.startsWith("/login/")) return "/"
  return url.pathname + url.search + url.hash
}
