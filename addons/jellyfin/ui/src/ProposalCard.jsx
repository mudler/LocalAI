/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The details of a Jellyfin change that waits for the person, inside the
// host's approval frame: what it does, to which player or item, for which
// user, and as which connection. The frame owns Confirm and Cancel; this card
// only draws. Names come from the server and are drawn as text.
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Notice } from '../../../internal/connector-ui/src/controls'
import { clock, connectionsFor, proposalView } from './proposal'
import './jellyfin.css'

function useConnections(server) {
  const [names, setNames] = useState(null)
  useEffect(() => {
    let live = true
    if (!server) return undefined
    connectorsApi.list().then((r) => { if (live) setNames(connectionsFor(r.connectors, server)) }).catch(() => { if (live) setNames([]) })
    return () => { live = false }
  }, [server])
  return names
}

function Row({ label, children, testId }) {
  return (
    <div className="jf-prop__row" data-testid={testId}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  )
}

export default function ProposalCard({ tool, effects, arguments: args }) {
  const { t } = useTranslation('jellyfin')
  const v = proposalView(tool, args, effects?.sample)
  const names = useConnections(v.server)
  const host = v.server.split('#')[0]
  const asText = names === null || names.length === 0
    ? t('approval.asUnknown', { server: host })
    : names.length === 1 ? t('approval.asOne', { name: names[0] }) : t('approval.asFirst', { server: host })
  const control = v.tool === 'control-playback'
  const title = control ? t(`approval.command.${v.command}`, { defaultValue: v.command }) : t(`approval.action.${v.tool}`, { defaultValue: v.tool })
  return (
    <div className="jf-prop" data-testid="jellyfin-proposal">
      <h3 className="jf-prop__title">{title}</h3>
      <dl className="jf-prop__facts">
        {control && (
          <Row label={t('approval.player')} testId="jellyfin-proposal-player">
            {v.device || '?'}
            <span className="jf-prop__target">{[v.client, v.user && t('approval.ofUser', { user: v.user })].filter(Boolean).join(', ')}</span>
          </Row>
        )}
        {control && v.nowPlaying && (
          <Row label={t('approval.nowPlaying')} testId="jellyfin-proposal-now">
            {v.nowPlaying}
            {v.positionSeconds !== null && <span className="jf-prop__target">{t('approval.at', { time: clock(v.positionSeconds) })}</span>}
          </Row>
        )}
        {control && v.command === 'play' && v.item && <Row label={t('approval.item')}>{v.item}</Row>}
        {control && v.toSeconds !== null && (v.command === 'seek' || v.command === 'play') && <Row label={v.command === 'seek' ? t('approval.seekTo') : t('approval.startAt')}>{clock(v.toSeconds)}</Row>}
        {control && v.command === 'set-volume' && v.volume !== null && <Row label={t('approval.volume')}>{v.volume}%</Row>}
        {(v.tool === 'mark-played' || v.tool === 'set-favorite') && (
          <>
            <Row label={t('approval.item')} testId="jellyfin-proposal-item">
              {v.item}
              {v.series && <span className="jf-prop__target">{v.series}</span>}
            </Row>
            <Row label={t('approval.change')}>
              {v.tool === 'mark-played'
                ? (v.played ? t('approval.markPlayed') : t('approval.markUnplayed'))
                : (v.favorite ? t('approval.addFavorite') : t('approval.removeFavorite'))}
            </Row>
          </>
        )}
        {v.tool === 'add-to-playlist' && (
          <>
            <Row label={t('approval.playlist')} testId="jellyfin-proposal-playlist">{v.playlist}</Row>
            <Row label={t('approval.items', { count: v.itemCount })}>
              <ul className="jf-prop__list">{v.items.map((n, i) => <li key={`${i}-${n}`}>{n}</li>)}</ul>
              {v.itemCount > v.items.length && <span className="jf-prop__target">{t('approval.more', { count: v.itemCount - v.items.length })}</span>}
            </Row>
          </>
        )}
        {!control && v.forUser && <Row label={t('approval.forUser')}>{v.forUser}</Row>}
        <Row label={t('approval.as')} testId="jellyfin-proposal-as">{asText}</Row>
      </dl>
      {control && <Notice tone="warn" role="note" testId="jellyfin-proposal-others">{t('approval.others')}</Notice>}
      <p className="cn-muted">{t('approval.names')}</p>
    </div>
  )
}
