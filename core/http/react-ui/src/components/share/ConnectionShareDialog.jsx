// SPDX-License-Identifier: MIT
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import ShareDialog from './ShareDialog'
import { kindShareClient } from './shareClients'
import { connectionShareStart } from './connectionShare'
import { chatConnectorsApi, sharesApi } from '../../utils/api'

// The Share dialog of one connector binding, for the page of an add-on
// (sdk.ui.ConnectionShareDialog). The host makes the binding shareable and
// calls the sharing routes itself: the add-on gives the ID of the binding and
// its own words, and never gets a client, a route or the confirmation header.
// The add-on's own code cannot reach the sharing routes (sdk.core.fetch
// refuses them), so only the person's clicks in this dialog share a binding.
export default function ConnectionShareDialog({ binding, name, roleLabel, roleHelp, title, intro, onChanged, onLost, close, testId }) {
  const { t } = useTranslation('common')
  const [state, setState] = useState({ binding: '', resourceId: '' })
  useEffect(() => {
    let live = true
    connectionShareStart(chatConnectorsApi, binding).then(
      (resourceId) => { if (live) setState({ binding, resourceId }) },
      (err) => { if (live) { setState({ binding, resourceId: '' }); onLost?.(err) } },
    )
    return () => { live = false }
    // onLost is the page's callback; a new one does not start another share.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [binding])
  const resourceId = state.binding === binding ? state.resourceId : ''
  const client = useMemo(() => (resourceId ? kindShareClient(sharesApi, 'connection', resourceId) : null), [resourceId])
  if (!client) return null
  return (
    <ShareDialog
      client={client}
      resource={{ id: resourceId, name: typeof name === 'string' ? name : '' }}
      roles={[{ id: 'use', label: roleLabel || t('share.roles.use'), help: roleHelp || t('share.roleHelp.use') }]}
      allowEveryone={false}
      title={title}
      intro={intro}
      onChanged={onChanged}
      onLost={onLost}
      close={close}
      testId={testId}
    />
  )
}
