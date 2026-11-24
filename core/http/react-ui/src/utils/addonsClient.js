// The browser's client of the add-on routes, bound to the page's base path.
import { apiUrl } from './basePath'
import { createAddonsApi, createAddonsCatalogAdapter, normalizeOperation } from './addonsApi'
import { createHostGrantsApi } from './hostGrantsApi'
import { createAddonFilesApi } from './addonFilesApi'

export const addonsApi = createAddonsApi({ base: apiUrl })
export const addonsCatalog = createAddonsCatalogAdapter(addonsApi)
export const addonFilesApi = createAddonFilesApi(addonsApi)
export const hostGrantsApi = createHostGrantsApi(addonsApi)
export { normalizeOperation }
