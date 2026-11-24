/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { lazy } from 'react'
import NetworkRow from '../components/chat/NetworkRow'
import { networkMode } from '../utils/chatEgress'
import { NETWORK_POLICY, SANDBOX_PROFILES, TOOL_PROVIDERS } from './hostControls'

// The components of each host control, by placement. The host owns all of
// them: an add-on that the apps list entitles to a control gets it drawn on
// its pages, and supplies nothing. A placement component takes host data
// only:
//
//   settings     { app, settings, initial, onChange, pending }: a block on the
//                add-on's Settings tab, drawn inside one host settings form
//   chat.tools   { egress }: a row of the chat Tools menu, from
//                GET /api/chats/{id}/egress
//
// The settings blocks load on demand, so the chat page does not carry them.
const NetworkPolicySettings = lazy(() => import('../components/hostcontrols/NetworkPolicySettings'))
const ToolProvidersSettings = lazy(() => import('../components/hostcontrols/ToolProvidersSettings'))
const SandboxProfilesSettings = lazy(() => import('../components/hostcontrols/SandboxProfilesSettings'))

export const HOST_CONTROLS = Object.freeze({
  [NETWORK_POLICY]: Object.freeze({ settings: NetworkPolicySettings, 'chat.tools': NetworkRow }),
  [TOOL_PROVIDERS]: Object.freeze({ settings: ToolProvidersSettings }),
  [SANDBOX_PROFILES]: Object.freeze({ settings: SandboxProfilesSettings }),
})

// The network of the chat in its Tools menu. The server names in egress.addon
// the add-on that runs the chat's commands when that add-on has the
// network-policy control; its row is then drawn. Commands that run on this
// machine get the host's own line, which needs no add-on.
export function ChatToolsControls({ egress }) {
  const mode = networkMode(egress)
  if (mode === 'host' || mode === 'unverified') return <NetworkRow egress={egress} />
  if (mode !== 'row') return null
  const Row = HOST_CONTROLS[NETWORK_POLICY]['chat.tools']
  return <Row egress={egress} />
}
