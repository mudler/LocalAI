/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import { apiUrl } from '../../utils/basePath'
import { AppIcon } from './AddonBits'

// One tile of the drawer: icon, name, state and the pin control. A running
// tile opens the add-on; every other tile opens the detail view. The pin
// control is a real button with a name and a state.
export default function AddonTile({ tile, pinned, onPin, onMove, onOpenDetail }) {
  const { t } = useTranslation('addons')
  const { t: tn } = useTranslation('nav')
  const title = tile.titleKey ? tn(tile.titleKey) : tile.title
  const stateText = tile.state === 'running' ? t('drawer.running') : t('drawer.stopped')
  const line = tile.statusLine ? t(`drawer.status.${tile.statusLine}`) : ''
  const body = (
    <>
      {tile.source === 'builtin'
        ? <span className="ad-icon" aria-hidden="true"><Icon name={tile.iconName} /></span>
        : <AppIcon src={tile.icon ? apiUrl(tile.icon) : ''} />}
      <span className="ad-tile__text">
        <span className="ad-tile__title">{title}</span>
        <span className="ad-tile__state" data-state={tile.state}>{stateText}{line ? `, ${line}` : ''}</span>
      </span>
    </>
  )
  return (
    <li className="ad-tile" data-testid="addon-tile" data-app={tile.id} data-state={tile.state}>
      {tile.target
        ? <Link className="ad-tile__open" to={tile.target}>{body}</Link>
        : <button type="button" className="ad-tile__open" onClick={() => onOpenDetail(tile.id)} aria-label={`${title}, ${stateText}`}>{body}</button>}
      {pinned && (
        <span className="ad-tile__order">
          <button type="button" className="ad-tile__move" data-testid="pin-earlier" aria-label={t('drawer.moveEarlier', { name: title })} onClick={() => onMove(tile.id, -1)}><Icon name="chevron-up" aria-hidden="true" /></button>
          <button type="button" className="ad-tile__move" data-testid="pin-later" aria-label={t('drawer.moveLater', { name: title })} onClick={() => onMove(tile.id, 1)}><Icon name="chevron-down" aria-hidden="true" /></button>
        </span>
      )}
      <button
        type="button"
        className="ad-tile__pin"
        data-testid="pin-toggle"
        aria-pressed={pinned}
        aria-label={pinned ? t('drawer.unpin', { name: title }) : t('drawer.pin', { name: title })}
        onClick={() => onPin(tile.id)}
      >
        <Icon name="star" aria-hidden="true" />
      </button>
    </li>
  )
}
