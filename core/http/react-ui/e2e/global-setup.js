import { buildStudioAddons } from './studio-addon.js'

// Builds the Studio and Talk add-on bundles once, before the workers start.
export default async function globalSetup() {
  await buildStudioAddons()
}
