// The desktop side of the config store: the shared store (core/configStore)
// over launcher.json on disk. No Electron import.
//
// `ConfigStore.open({ home, ... })` keeps the call shape the main process and
// its tests have always used; the store it returns is the shared class.
import { loadConfig, saveConfig, type ConfigOptions, type LauncherConfig } from './config'
import { ConfigStore as CoreConfigStore, type ConfigIO } from '../core/configStore'

// Every accessor and key constant. The local ConfigStore below takes the place
// of the class.
export * from '../core/configStore'

export interface ConfigStoreOptions extends ConfigOptions {
  /** Replaces saveConfig, for tests. */
  save?: (config: LauncherConfig, opts: ConfigOptions) => Promise<void>
}

/** launcher.json as a ConfigIO: load and save with the given options. */
export function nodeConfigIO(opts: ConfigStoreOptions): ConfigIO {
  const save = opts.save ?? saveConfig
  return {
    load: () => loadConfig(opts),
    save: (c) => save(c, opts),
  }
}

export type ConfigStore = CoreConfigStore
export const ConfigStore = {
  open: (opts: ConfigStoreOptions = {}): Promise<CoreConfigStore> => CoreConfigStore.open(nodeConfigIO(opts)),
}
