import { consoles, consolePaths } from '../components/console/consoleConfig'

// Inline "Create" group from the sidebar (these pages live outside a console).
const CREATE_PATHS = ['/app/chat', '/app/studio', '/app/talk']

// The section/console an app page belongs to, returned as a `nav` i18n key for
// use as the PageHeader eyebrow. Console pages map to their console title
// (Build / Operate); the inline Create group maps to sections.create; any other
// top-level page (Home, Install Models, Account, ...) has no eyebrow.
export function sectionKeyForPath(pathname) {
  for (const c of consoles) {
    if (consolePaths(c).some(p => pathname === p || pathname.startsWith(p + '/'))) {
      return c.titleKey
    }
  }
  if (CREATE_PATHS.some(p => pathname === p || pathname.startsWith(p + '/'))) return 'sections.create'
  return null
}
