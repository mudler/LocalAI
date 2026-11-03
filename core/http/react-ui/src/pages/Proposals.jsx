/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useRef } from 'react'
import { useTranslation } from 'react-i18next'
import ChangeProposalCard from '../components/chat/ChangeProposalCard'
import { EmptyState, LoadError, Page, PageHeader, SkeletonRows } from '../components/ui'
import { usePendingChanges } from '../hooks/usePendingChanges'
import './chats.css'

// The changes that models proposed and that wait for an answer, from every
// chat and every connector, each as the card the chat draws. A card that was
// answered stays until the list is read again, so the receipt can be read and
// Undo used.
export default function Proposals() {
  const { t } = useTranslation('chats')
  const pending = usePendingChanges()
  const { status, error, refresh } = pending
  // A card stays on the page after it is answered, with its receipt, until the
  // page is left; the list the server gives only holds the ones that wait.
  const seen = useRef(new Map())
  for (const p of pending.items) if (!seen.current.has(p.id)) seen.current.set(p.id, p)
  const items = Array.from(seen.current.values())
  return (
    <Page className="pp-page" data-testid="proposals-page">
      <PageHeader title={t('proposalsPage.title')} supporting={t('proposalsPage.supporting')} />
      {status === 'loading' && <SkeletonRows count={3} />}
      {status === 'error' && <LoadError title={t('proposalsPage.loadError')} error={error} onRetry={refresh} testId="proposals-load-error" />}
      {status === 'ready' && items.length === 0 && (
        <EmptyState variant="no-results" icon="check-circle" title={t('proposalsPage.emptyTitle')} body={t('proposalsPage.emptyBody')} testId="proposals-empty" />
      )}
      {items.length > 0 && (
        <ul className="pp-list" aria-label={t('proposalsPage.title')}>
          {items.map((p) => <li key={p.id} className="pp-item"><ChangeProposalCard id={p.id} initial={p} t={t} /></li>)}
        </ul>
      )}
    </Page>
  )
}
