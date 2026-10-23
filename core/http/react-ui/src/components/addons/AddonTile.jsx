/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { apiUrl } from '../../utils/basePath'
import { AppIcon, UpdateChip } from './AddonBits'
import AddonMenu from './AddonMenu'
import { addedByAddon } from './addonState'

// One add-on card: icon, name, state, what it does and what it adds. The card
// body selects the add-on (its preview opens); a running add-on with a page
// also has an Open link. Pinning, reordering, details and removal sit in the
// overflow menu, so nothing on the card is a bare icon.
export default function AddonTile({ tile, pinned, selected = false, onPin, onMove, onOpenDetail, onRemove }) {
  const { t } = useTranslation('addons')
  const { t: tn } = useTranslation('nav')
  const title = tile.titleKey ? tn(tile.titleKey) : tile.title
  const stateText = tile.state === 'running' ? t('drawer.running') : t('drawer.stopped')
  const line = tile.statusLine ? t(`drawer.status.${tile.statusLine}`) : ''
  const added = tile.app ? addedByAddon(tile.app) : { page: tile.source === 'builtin', tools: 0 }
  return (
    <li className="ad-tile" data-testid="addon-tile" data-app={tile.id} data-state={tile.state} data-selected={selected ? 'true' : undefined}>
      <button
        type="button"
        className="ad-tile__open"
        aria-pressed={selected}
        aria-label={`${title}, ${stateText}${line ? `, ${line}` : ''}`}
        onClick={() => onOpenDetail(tile.id)}
      >
        {tile.source === 'builtin'
          ? <span className="ad-icon ad-icon--empty" aria-hidden="true"><Icon name={tile.iconName} /></span>
          : <AppIcon src={tile.icon ? apiUrl(tile.icon) : ''} />}
        <span className="ad-tile__text">
          <span className="ad-tile__title">{title}</span>
          <span className="ad-tile__state" data-state={tile.state}>{stateText}{line ? `, ${line}` : ''}</span>
          {tile.app && tile.app.update === 'update-pending' && <span className="ad-tile__update"><UpdateChip /></span>}
          {tile.description && <span className="ad-tile__desc">{tile.description}</span>}
          {(added.page || added.tools > 0) && (
            <span className="ad-tile__adds" data-testid="tile-adds">
              {added.page && <span className="ad-tile__add"><Icon name="file" aria-hidden="true" /> {t('card.addsPage')}</span>}
              {added.tools > 0 && <span className="ad-tile__add"><Icon name="wrench" aria-hidden="true" /> {t('card.chatTools', { count: added.tools })}</span>}
            </span>
          )}
        </span>
      </button>
      <div className="ad-tile__side">
        <AddonMenu
          name={title}
          pinned={pinned}
          onPin={() => onPin(tile.id)}
          onMove={pinned ? (delta) => onMove(tile.id, delta) : undefined}
          onDetails={() => onOpenDetail(tile.id)}
          onRemove={onRemove && tile.app ? () => onRemove(tile) : undefined}
        />
      </div>
      {tile.target && (
        <div className="ad-tile__foot">
          <Link className="dk-btn dk-btn--sm" to={tile.target} aria-label={t('card.openNamed', { name: title })}>
            <Icon name="external-link" /> {t('apps.open')}
          </Link>
        </div>
      )}
    </li>
  )
}
