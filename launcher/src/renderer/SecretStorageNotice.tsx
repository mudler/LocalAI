// Says where secrets go when it is not the OS secure storage: only for this
// session (no keyring), a plain file (the user's choice), or values that
// cannot be read now (a locked keyring). Nothing to say for the keychain.
import { Callout } from '@localai/ui-kit'
import type { SecretStorageView } from '../shared/types'
import { S } from './strings'

export function SecretStorageNotice({ storage, className }: { storage?: SecretStorageView; className?: string }) {
  if (!storage) return null
  if (storage.unreadable > 0) {
    return (
      <Callout tone="warn" title={S.secrets.locked(storage.unreadable)} className={className} testId="secrets-notice">
        {S.secrets.lockedBody}
      </Callout>
    )
  }
  if (storage.backend === 'session') {
    return (
      <Callout tone="warn" title={S.secrets.sessionTitle} className={className} testId="secrets-notice">
        {S.secrets.sessionBody}
      </Callout>
    )
  }
  if (storage.backend === 'plaintext-file') {
    return (
      <Callout tone="info" title={S.secrets.plainTitle} className={className} testId="secrets-notice">
        {S.secrets.plainBody}
      </Callout>
    )
  }
  return null
}
