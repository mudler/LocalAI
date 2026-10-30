// What shows while code or data for a screen is still on its way. The shell
// comes first: a cold start draws the sidebar and the page frame at once and
// puts skeletons where the content will be, never a blank window with a
// centred spinner.

// Content-area placeholder for a lazy route: a title, a supporting line and
// three rows, in the page frame. It appears after 200 ms, so a fast chunk
// load never flashes it. The sidebar and header stay mounted around it.
export default function RouteFallback() {
  return (
    <div className="route-fallback page page--content" role="status" aria-busy="true" aria-label="Loading">
      <PagePlaceholder />
    </div>
  )
}

function PagePlaceholder() {
  return (
    <div className="route-fallback__body" aria-hidden="true">
      <span className="skeleton skeleton--title" />
      <span className="skeleton skeleton--line skeleton--w60" />
      <div className="skeleton-rows">
        {[0, 1, 2].map(i => (
          <div className="skeleton-row" key={i}>
            <span className="skeleton skeleton--tile" />
            <span className="skeleton-row__lines">
              <span className="skeleton skeleton--line skeleton--w60" />
              <span className="skeleton skeleton--line skeleton--w40" />
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

function sidebarCollapsed() {
  try { return localStorage.getItem('localai_sidebar_collapsed') === 'true' } catch { return false }
}

// The boot screen: the app shell before the translations and the session
// have loaded. It draws the sidebar column (collapsed if the person left it
// so), the phone header, and the content placeholder, so the first paint
// already has the layout of the app. `embedded` keeps it in the flow, for the
// component catalog.
export function ShellFallback({ embedded = false }) {
  const collapsed = sidebarCollapsed()
  return (
    <div
      className={`app-shell-fallback${collapsed ? ' is-collapsed' : ''}${embedded ? ' is-embedded' : ''}`}
      role="status"
      aria-busy="true"
      aria-label="Loading"
      data-testid="shell-fallback"
    >
      <div className="app-shell-fallback__side" aria-hidden="true">
        <span className="skeleton skeleton--logo" />
        {[0, 1, 2, 3, 4, 5].map(i => <span key={i} className="skeleton skeleton--nav" />)}
      </div>
      <div className="app-shell-fallback__top" aria-hidden="true">
        <span className="skeleton skeleton--icon" />
        <span className="skeleton skeleton--line skeleton--w40" />
      </div>
      <div className="app-shell-fallback__main page page--content">
        <PagePlaceholder />
      </div>
    </div>
  )
}
