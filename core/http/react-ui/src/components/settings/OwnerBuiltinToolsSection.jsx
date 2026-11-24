/* eslint-disable no-unused-vars -- JSX components */
import { useMemo } from 'react'
import BuiltinToolsSection from './BuiltinToolsSection'
import { createHostSettingsClient } from '../../utils/hostSettingsClient'
import { createAddonsApi } from '../../utils/addonsApi'
import { apiUrl } from '../../utils/basePath'
import { useHostSettings } from '../../hooks/useHostSettings'

export default function OwnerBuiltinToolsSection(props) {
  const client = useMemo(() => createHostSettingsClient({ api: createAddonsApi({ base: apiUrl }) }), [])
  const owner = useHostSettings(client, 'settings-tool-providers')
  const save = () => {
    const before = owner.snapshot.policy.chats_tool_providers
    const after = JSON.parse(owner.draft.chats_tool_providers_json ?? JSON.stringify(owner.draft.chats_tool_providers))
    const patch = Object.fromEntries([...new Set([...Object.keys(before), ...Object.keys(after)])].filter(k => before[k] !== after[k]).map(k => [k, after[k] ?? null]))
    return owner.save({ chats_tool_providers: patch })
  }
  return <>
    <BuiltinToolsSection {...props} providerSettings={owner.draft || { chats_tool_providers: {} }}
      onProviderChange={owner.setDraft} providersDisabled={!owner.snapshot || owner.busy || !!owner.error} />
    {owner.error && <div role="alert">Owner settings unavailable or changed: {owner.error.message}
      <button type="button" onClick={owner.reload} disabled={owner.busy}>Reload and rebase edits</button>
      {owner.uncertain && <button type="button" onClick={() => owner.save({})} disabled={owner.busy}>Retry same operation</button>}
    </div>}
    {owner.snapshot && <button type="button" disabled={owner.busy || !!owner.error} onClick={save}>Apply provider choices</button>}
  </>
}
