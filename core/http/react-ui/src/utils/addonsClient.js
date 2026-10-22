// The browser's client of the add-on routes, bound to the page's base path.
import { apiUrl } from './basePath'
import { createAddonsApi, createAddonsCatalogAdapter } from './addonsApi'

export const addonsApi = createAddonsApi({ base: apiUrl })
export const addonsCatalog = createAddonsCatalogAdapter(addonsApi)
