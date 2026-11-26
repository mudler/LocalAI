// SPDX-License-Identifier: MIT
// The Android back button. Order: close a popover, sheet or dialog; go back in
// the WebUI history; close a shell screen; leave the app. Leaving minimizes
// and never exits, so the P2P connection keeps its background grace time.
import type { WebuiViewState } from '@launcher/shared/types'

export type BackAction = 'escape' | 'webui-back' | 'close-screen' | 'minimize'

export interface BackState {
  overlayOpen: boolean
  shellScreenOpen: boolean
  webuiVisible: boolean
  webuiCanGoBack: boolean
}

export function decideBack(s: BackState): BackAction {
  if (s.overlayOpen) return 'escape'
  if (s.webuiVisible && s.webuiCanGoBack) return 'webui-back'
  if (s.shellScreenOpen) return 'close-screen'
  return 'minimize'
}

export interface BackCore {
  webuiState(): Pick<WebuiViewState, 'overlay' | 'screen' | 'layout'>
  /** True when the WebUI went back a page. */
  webuiGoBack(): Promise<boolean>
}

/** Runs one press of the back button and says what it did. */
export async function runBack(core: BackCore, env: { doc: Document; minimize(): Promise<void> }): Promise<BackAction> {
  const st = core.webuiState()
  const s: BackState = {
    // The shell tells the core about popovers; its dialogs are found in the page.
    overlayOpen: st.overlay || env.doc.querySelector('[aria-modal="true"]') !== null,
    shellScreenOpen: st.screen !== null,
    webuiVisible: st.layout === 'webui',
    // Only the WebUI view knows its history: ask it when it comes to that.
    webuiCanGoBack: true,
  }
  let action = decideBack(s)
  if (action === 'webui-back') {
    if (await core.webuiGoBack()) return action
    action = decideBack({ ...s, webuiCanGoBack: false })
  }
  switch (action) {
    case 'escape': {
      const target = env.doc.activeElement ?? env.doc.body
      target.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', code: 'Escape', bubbles: true, cancelable: true }))
      return action
    }
    case 'close-screen': {
      const back = env.doc.querySelector<HTMLElement>('[data-testid="back"]')
      if (back) {
        back.click()
        return action
      }
      break
    }
  }
  await env.minimize()
  return 'minimize'
}
