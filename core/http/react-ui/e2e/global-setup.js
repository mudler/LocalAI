import { buildStudioAddons } from './studio-addon.js'

// Builds the add-on bundles the specs serve once, before the workers start.
export default async function globalSetup() {
  await buildStudioAddons()
}
