// SPDX-License-Identifier: MIT
/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useMemo, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import ShareDialog from '../share/ShareDialog'
import { pageShareClient } from '../share/shareClients'
import { pageSharesApi } from '../../utils/api'

// The Share dialog of one of the person's own pages: the common Share dialog
// with the roles of a page and the words of Pages. The page stays in its
// folder; the people added open it from "Shared with you" and are asked to
// allow each version of its code.
//
//   page       { name, title }
//   sharing    false without user accounts: the dialog says so and calls nothing
//   onChanged  (summary) after a change or a read, with { people, everyone,
//              total, allowed, hash } as last read, so the list can show the
//              count at once. When an administrator turned sharing off (the
//              grants say enabled false) no one can be added; the people who
//              already have access can still be removed
//   onLost     the person cannot share this page here any more
//   close
export const EVERYONE_ROLES = ['view', 'use']

// What a list shows about the grants of a page.
export function shareSummary(payload) {
  const grants = Array.isArray(payload?.grants) ? payload.grants : []
  const everyone = grants.some(g => g.userId === '@everyone')
  const people = grants.length - (everyone ? 1 : 0)
  const allowed = Number.isFinite(payload?.allowed) ? payload.allowed : null
  return { people, everyone, total: grants.length, allowed, hash: typeof payload?.hash === 'string' ? payload.hash : '', enabled: payload?.enabled !== false }
}

export default function PageShareDialog({ page, sharing = true, onChanged, onLost, close }) {
  const { t } = useTranslation('pages')
  const changedRef = useRef(onChanged)
  changedRef.current = onChanged
  // The grants say whether an administrator turned sharing off.
  const [off, setOff] = useState(false)
  const live = useRef(true)
  useEffect(() => { live.current = true; return () => { live.current = false } }, [])
  // The client is read through so the list learns every answer at once.
  const client = useMemo(() => {
    const base = pageShareClient(pageSharesApi, page.name)
    const tap = (p) => p.then((out) => {
      if (out && Array.isArray(out.grants)) {
        if (live.current) setOff(out.enabled === false)
        changedRef.current?.(shareSummary(out))
      }
      return out
    })
    return Object.freeze({
      grants: (o) => tap(base.grants(o)),
      setGrant: (principal, role, o) => tap(base.setGrant(principal, role, o)),
      revoke: (principal, o) => tap(base.revoke(principal, o)),
      searchUsers: base.searchUsers,
    })
  }, [page.name])
  const roles = useMemo(() => [
    { id: 'view', label: t('share.role.view'), help: t('share.roleHelp.view') },
    { id: 'use', label: t('share.role.use'), help: t('share.roleHelp.use') },
    { id: 'edit', label: t('share.role.edit'), help: t('share.roleHelp.edit') },
  ], [t])
  const summary = useMemo(() => (payload) => {
    const s = shareSummary(payload)
    if (s.people === 0 && !s.everyone) return ''
    const who = s.everyone
      ? (s.people > 0 ? t('share.count.everyoneAnd', { count: s.people }) : t('share.count.everyone'))
      : t('share.count.people', { count: s.people })
    return s.allowed === null ? who : `${who} ${t('share.count.allowed', { count: s.allowed })}`
  }, [t])

  return (
    <ShareDialog
      client={client}
      resource={{ id: page.name, name: page.title || page.name }}
      roles={roles}
      allowEveryone
      sharing={sharing}
      everyoneRoles={EVERYONE_ROLES}
      intro={t('share.intro')}
      warning={t('share.warning')}
      summary={summary}
      addOff={off ? t('share.offAdmin') : ''}
      roleNotes={{ edit: t('share.editNote') }}
      onLost={onLost}
      close={close}
      testId="page-share-dialog"
    />
  )
}
