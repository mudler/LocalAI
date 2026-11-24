/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import Dialog from '../Dialog'
import { Button, Callout, SkeletonRows, Tag } from '../ui'
import { upgradeFailureWords } from '../addons/updateReview'
import { reviewGroups, riskText, startItems } from '../../utils/upgradeRun'
import { originText } from './UpdateRow'
import '../upgrade/upgrade.css'

// Why an item cannot start, in words: the reason of the plan (held,
// edited_keys, in_run, no_update, installs_disabled) through the one
// failure-words map, never the raw code.
function blockedText(item, t, ta) {
  const reason = item.reason ? upgradeFailureWords(item.reason, ta, '', 'run').trim().replace(/[.\s]+$/, '') : t('row.noReason')
  return t('review.blocked', { reason })
}

// The lines under an item: its risks, why it cannot start, and for a model the
// reason it is not selected by default.
function linesOf(item, t) {
  const out = (Array.isArray(item.risk) ? item.risk : []).map(r => riskText(r, t)).filter(Boolean)
  if (item.kind === 'model' && item.startable && !item.default_selected) out.push(t('review.modelNote'))
  return out
}

// The review of an update run: what changes, in which order, and the choice
// of items. Nothing starts until the primary button. The run that follows is
// the server's, so closing the page after the start does not stop it. With
// one item (a row's Update) it is the short question of that item.
//
//   review    the review state of useUpgradeRun
//   rowsByKey the page's rows by key, for the names and origins the page shows
export default function ReviewDialog({ review, rowsByKey, busy, onToggle, onSelectAll, onSelectNone, onStart, onFollow, onClose }) {
  const { t } = useTranslation('updates')
  const { t: ta } = useTranslation('addons')
  if (!review) return null
  const rowOf = (item) => (rowsByKey && rowsByKey.get(item.key)) || null
  const nameOf = (item) => (rowOf(item) && rowOf(item).name) || item.title || item.name
  const versions = (item) => t('row.versions', { from: item.from || '?', to: item.to || '?' })
  const count = startItems(review).length
  const starting = busy === 'start'
  const loading = review.status === 'loading'
  // A review that did not load says why in its own callout; the refusal is
  // the answer to a start.
  const refused = review.status !== 'error' && (review.error || review.code)
    ? upgradeFailureWords(review.code || '', ta, review.error, 'run')
    : ''
  const refusal = refused && (
    <Callout tone="error" role="alert" testId="update-review-refused">{refused}</Callout>
  )
  const failedLoad = review.status === 'error' && (
    <Callout tone="error" role="alert" testId="update-review-error" title={t('review.loadError')}>
      {review.code ? upgradeFailureWords(review.code, ta, review.error, 'run') : (review.error || '')}
    </Callout>
  )
  // A run is already in progress: a start would be refused, so the review
  // offers that run instead.
  const active = Boolean(review.activeRun)
  const activeNote = active && (
    <Callout
      tone="warn"
      testId="update-review-active"
      actions={onFollow ? <Button size="sm" onClick={() => onFollow(review.activeRun)} data-testid="update-review-follow">{t('review.follow')}</Button> : null}
    >
      {t('run.busy')}
    </Callout>
  )

  if (review.single) {
    const item = review.items[0] || null
    const row = item ? rowOf(item) : null
    const lines = item ? linesOf(item, t) : []
    const origin = row ? originText(row, t) : ''
    return (
      <Dialog
        title={item ? t('review.singleTitle', { name: nameOf(item) }) : t('review.singleLoading')}
        onClose={onClose}
        pending={starting}
        closeLabel={t('review.cancel')}
        testId="update-review"
        labelId="update-review-title"
        foot={(
          <>
            <Button variant="ghost" onClick={onClose} disabled={starting}>{t('review.cancel')}</Button>
            <Button variant="primary" icon="arrow-up" busy={starting} disabled={loading || active || count === 0 || starting} onClick={onStart} data-testid="update-review-start">
              {t('review.startOne')}
            </Button>
          </>
        )}
      >
        {loading && <SkeletonRows count={2} label={t('review.loading')} />}
        {failedLoad}
        {activeNote}
        {item && (
          <div className="up-single" data-testid="update-review-item" data-key={item.key} data-startable={item.startable ? 'true' : 'false'}>
            <p className="up-row__versions">{origin ? `${versions(item)} · ${origin}` : versions(item)}</p>
            {item.startable
              ? <Callout testId="update-review-note">{[...lines, t('review.singleNote')].join(' ')}</Callout>
              : <Callout tone="warn" testId="update-review-blocked">{blockedText(item, t, ta)}</Callout>}
          </div>
        )}
        {refusal}
      </Dialog>
    )
  }

  const total = review.items.length
  const groups = reviewGroups(review.items)
  const startable = review.items.filter(i => i.startable).length
  return (
    <Dialog
      title={loading ? t('review.loadingTitle') : t('review.title', { count: total })}
      description={t('review.intro')}
      onClose={onClose}
      pending={starting}
      closeLabel={t('review.cancel')}
      testId="update-review"
      labelId="update-review-title"
      wide
      foot={(
        <>
          <Button variant="ghost" onClick={onClose} disabled={starting}>{t('review.cancel')}</Button>
          <Button variant="primary" icon="arrow-up" busy={starting} disabled={loading || active || count === 0 || starting} onClick={onStart} data-testid="update-review-start">
            {t('review.start', { count })}
          </Button>
        </>
      )}
    >
      {loading && <SkeletonRows count={4} label={t('review.loading')} />}
      {failedLoad}
      {activeNote}
      {review.status === 'ready' && (
        <>
          <div className="up-pick__bar">
            <Button size="sm" variant="ghost" disabled={startable === 0} onClick={onSelectAll} data-testid="update-review-all">{t('review.selectAll')}</Button>
            <Button size="sm" variant="ghost" disabled={review.selected.length === 0} onClick={onSelectNone} data-testid="update-review-none">{t('review.selectNone')}</Button>
            <span className="up-pick__count" aria-live="polite" data-testid="update-review-count">{t('review.selected', { count: review.selected.length, total })}</span>
          </div>
          <Callout testId="update-review-note">{t('review.order')}</Callout>
          {total === 0 && <p className="up-empty">{t('review.empty')}</p>}
          {groups.map(g => (
            <section key={g.id} className="up-group" aria-labelledby={`update-review-${g.id}`} data-group={g.id}>
              <h3 className="up-group__title" id={`update-review-${g.id}`}>{t(`review.group.${g.id}`, { count: g.items.length })}</h3>
              <ul className="up-list">
                {g.items.map(item => {
                  const name = nameOf(item)
                  const lines = linesOf(item, t)
                  return (
                    <li key={item.key} className="up-row" data-testid="update-review-item" data-key={item.key} data-startable={item.startable ? 'true' : 'false'}>
                      <input
                        className="dk-check" type="checkbox"
                        checked={review.selected.includes(item.key)}
                        disabled={!item.startable || starting}
                        onChange={() => onToggle(item.key)}
                        aria-label={t('review.item', { name })}
                      />
                      <div className="up-row__main">
                        <span className="up-row__name">
                          {name}
                          <span className="uc-row__tag">{t(`row.tag.${item.kind}`)}</span>
                          {item.kind === 'addon' && <Tag tone="warn" testId="update-review-backup">{t('review.backsUp')}</Tag>}
                        </span>
                        <span className="up-row__versions">{versions(item)}</span>
                        {lines.map(line => <span key={line} className="up-row__why" data-testid="update-review-risk">{line}</span>)}
                        {!item.startable && (
                          <span className="up-row__why" data-tone="warn" data-testid="update-review-why">{blockedText(item, t, ta)}</span>
                        )}
                      </div>
                    </li>
                  )
                })}
              </ul>
            </section>
          ))}
          {refusal}
        </>
      )}
    </Dialog>
  )
}
