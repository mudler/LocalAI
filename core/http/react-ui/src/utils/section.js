import { hubs, hubOwnsPath } from '../components/hub/hubConfig'

// Inline "Create" group from the sidebar (these pages live outside a hub).
const CREATE_PATHS = ['/app/chat', '/app/studio', '/app/talk']

// The section/hub an app page belongs to, returned as a `nav` i18n key for
// use as the PageHeader eyebrow. Hub pages map to their hub title
// (Build / Operate); the inline Create group maps to sections.create; any other
// top-level page (Home, Models, Account, ...) has no eyebrow.
export function sectionKeyForPath(pathname) {
  for (const h of hubs) {
    if (hubOwnsPath(h, pathname)) return h.titleKey
  }
  if (CREATE_PATHS.some(p => pathname === p || pathname.startsWith(p + '/'))) return 'sections.create'
  return null
}
