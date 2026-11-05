// PageNotFound is the not-found view of a global module page. darkraise-ui's
// EmptyState titles itself with an h3, which skips a level under the page
// header's h1, so this repeats its markup with an h2.
export function PageNotFound() {
  return (
    <div className="dr-empty-state">
      <h2 className="dr-empty-state-title">Page not found</h2>
      <p className="dr-empty-state-description">Nothing lives at this address.</p>
    </div>
  )
}
