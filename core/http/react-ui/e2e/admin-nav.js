// Locators of the Administration sidebar, shared by the specs that move
// between Administration pages (admin-sidebar.spec.js has the sidebar's own).
export const ADMIN_NAV = '[data-testid="admin-nav"]'

// The sidebar item that links to `path` (an item with a query is told apart by
// `query`, for example "tab=keys").
export const adminItem = (page, path, query) => page.locator(
  `${ADMIN_NAV} a[data-admin-item="${path}"]${query ? `[href$="?${query}"]` : ''}`,
)

// The item that is the current page.
export const adminCurrent = (page) => page.locator(`${ADMIN_NAV} a[aria-current="page"]`)

// The group of the sidebar: landing, machine, cluster, runtime, traffic, access, settings.
export const adminGroup = (page, id) => page.locator(`${ADMIN_NAV} [data-group="${id}"]`)

// Open the folded Traffic group when it is closed, so its pages can be clicked.
export async function openTraffic(page) {
  const toggle = adminGroup(page, 'traffic').getByRole('button', { name: /Traffic/ })
  if (await toggle.count() && await toggle.getAttribute('aria-expanded') === 'false') await toggle.click()
}
