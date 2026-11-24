import { useContext, useLayoutEffect } from 'react'
import Page from '../ui/Page'
import PageHeader from '../PageHeader'
import AdminCrumbs from './AdminCrumbs'
import { AdminShellContext } from './adminShellContext'

// The one template of an Administration page: the breadcrumb, a title with one
// primary action, an optional summary strip, the content, and the side sheet
// of the page's details. It is the kit's Page with PageHeader, so a page only
// says what it holds.
//
//   title, supporting   as PageHeader
//   action              the page's single primary <Button>
//   summary             an optional <SummaryStrip> under the header
//   sheet               the page's <SideSheet>, rendered after the content
//   width               "content" (default) or "full", as Page
export default function AdminPage({ title, supporting, action, summary, sheet, width = 'content', className = '', children, ...rest }) {
  const shell = useContext(AdminShellContext)
  const claim = shell?.claimCrumbs
  // The breadcrumb is in this header: the layout keeps its own away.
  useLayoutEffect(() => {
    if (!claim) return undefined
    claim(1)
    return () => claim(-1)
  }, [claim])
  return (
    <Page width={width} className={`admin-page ${className}`.trim()} {...rest}>
      <PageHeader crumbs={shell ? <AdminCrumbs /> : null} title={title} supporting={supporting} actions={action} />
      {summary}
      {children}
      {sheet}
    </Page>
  )
}

// A row of figures under the header: a label over a value and a small unit.
//   items  [{ key, label, value, unit, tone }]; tone is "warn" or "error"
export function SummaryStrip({ items, label }) {
  return (
    <dl className="admin-summary" aria-label={label}>
      {items.map(item => (
        <div key={item.key} className="admin-summary__item" data-tone={item.tone || undefined}>
          <dt>{item.label}</dt>
          <dd><strong>{item.value}</strong>{item.unit && <span>{item.unit}</span>}</dd>
        </div>
      ))}
    </dl>
  )
}
