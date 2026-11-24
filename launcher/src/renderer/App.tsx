import { BridgeContext, type Bridge } from './bridge'
import { Shell } from './Shell'

/** The shell renderer. `bridge` is window.launcher in the app, a fake in tests and the harness. */
export function App({ bridge, now }: { bridge: Bridge; now?: number }) {
  return (
    <BridgeContext.Provider value={bridge}>
      <Shell now={now} />
    </BridgeContext.Provider>
  )
}
