// SPDX-License-Identifier: MIT
/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import StatusChip from '../ui/StatusChip'
import { roleTone, shareLine } from './shareModel'
import './share.css'

// The caller's role on a resource, as one chip. A role is not a status, so
// only the owner stands out. `label` replaces the host's words (an add-on
// with roles of its own passes its own).
export function RoleBadge({ role, label, testId = 'role-badge' }) {
  const { t } = useTranslation('common')
  const text = label || t(`share.roles.${['owner', 'use', 'manage', 'viewer', 'editor'].includes(role) ? role : 'use'}`)
  return (
    <span className="share-role" data-role={role} data-testid={testId}>
      <StatusChip tone={roleTone(role)} icon={role === 'owner' ? 'user-shield' : 'users'}>{text}</StatusChip>
    </span>
  )
}

// What a list row shows about sharing: the caller's role and one line, for
// example "Shared by Alice", "Shared with everyone by Alice", "Shared with 2"
// or "Private". `entry` is a row of a share listing: owned rows carry
// role 'owner' and sharedWith, shared rows carry role, owner and everyone.
//
//   <SharedByBadge entry={{ role: 'use', owner: 'Alice' }} />
//   <SharedByBadge entry={row} roleLabel={t('role.editor')} hideRole />
export function SharedByBadge({ entry, roleLabel, hideRole = false, testId = 'shared-by' }) {
  const { t } = useTranslation('common')
  const line = shareLine(entry)
  if (!entry || (hideRole && !line)) return null
  return (
    <span className="share-badge" data-testid={testId} data-role={entry.role}>
      {hideRole ? null : <RoleBadge role={entry.role} label={roleLabel} />}
      {line && <span className="share-badge__line" data-testid={`${testId}-line`}>{t(line.key, line.vars)}</span>}
    </span>
  )
}
