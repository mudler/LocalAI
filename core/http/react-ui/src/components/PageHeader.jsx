import { ViewAsChip } from './viewas/parts'
import { useViewAs } from '../contexts/ViewAsContext'

// Page header: title, supporting line, and a right-aligned actions slot.
// There is no eyebrow above the title: the sidebar already says where the page
// lives, so the title stands alone. An Administration page (AdminPage) passes
// `crumbs`, its breadcrumb trail, which sits above the title.
//
// One title style for every page (28/34 semibold, 24/30 on a phone), the
// supporting line up to 64 characters, the actions aligned to the bottom
// right, and 24 px to the content. Put it inside <Page> (components/ui/Page)
// so every page shares one left edge and one width. See ui/README.md.
//
//   title       the page name, as the sidebar says it
//   supporting  one sentence: what the page is for
//   actions     the page's primary <Button> (and at most two others)
//   titleId     an id for the h1, to label a region with it
//   crumbs      a breadcrumb trail above the title (Administration pages)
export default function PageHeader({ title, supporting, actions, className = '', titleId, crumbs }) {
  // While "View as" is on, a page that follows it draws a chip beside its title.
  const { active } = useViewAs()
  const heading = title && <h1 className="page-title" id={titleId}>{title}</h1>
  return (
    <header className={`page-header page-header--editorial ${className}`.trim()}>
      <div className="page-header__lead">
        {crumbs}
        {active && title ? <div className="page-title-row">{heading}<ViewAsChip /></div> : heading}
        {supporting && <p className="page-header__supporting">{supporting}</p>}
      </div>
      {actions && <div className="page-header__meta">{actions}</div>}
    </header>
  )
}
