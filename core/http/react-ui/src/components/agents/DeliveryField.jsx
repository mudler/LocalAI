// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
import ReportsTo from './ReportsTo'
import { CONNECTIONS_PATH } from '../../utils/connectionsTabs'
import { DELIVERY_MODES, deliveryModeLabel, deliveryOptionLabel } from '../../utils/triggerDelivery'

// Where the runs of a trigger report, on the trigger form: the Inbox, the chat
// a messaging app proposed it in, or a channel the owner approved on one of
// the owner's connections, and when to report. The options come from the
// server (GET /api/chats/triggers/destinations), so every connector that can
// receive a report shows the same way and no service is named here.
//   value     { key, mode }, see utils/triggerDelivery
//   options   deliveryOptions(...)
//   unusable  unusableConnections(...): connections that offer no channel
//   trigger   the stored trigger, to show where it reports now; null for a new one
//   fromChat  the trigger was proposed in a chat of a messaging app
//   error     the destinations could not be read
export default function DeliveryField({ value, onChange, options, unusable = [], trigger = null, fromChat = false, error = null, icons = {} }) {
  const { t } = useTranslation('agents')
  // A channel connection is the group of its channels in the picker.
  const groups = []
  for (const o of options) {
    if (o.kind !== 'channel') continue
    const name = o.connection && o.service ? `${o.connection} (${o.service})` : (o.connection || o.service)
    let g = groups.find((x) => x.name === name)
    if (!g) groups.push(g = { name, options: [] })
    g.options.push(o)
  }
  const plain = options.filter((o) => o.kind !== 'channel')
  return (
    <div className="aj-form" data-testid="aj-delivery">
      {trigger && (
        <div className="dk-field">
          <span className="dk-label">{t('jobs.edit.delivery.now')}</span>
          <ReportsTo trigger={trigger} icons={icons} />
        </div>
      )}
      <div className="aj-row2">
        <div className="dk-field">
          <label className="dk-label" htmlFor="aj-delivery-where">{t('jobs.edit.delivery.where')}</label>
          <span className="dk-select-wrap">
            <select id="aj-delivery-where" className="dk-select" value={value.key} data-testid="aj-delivery-where"
              aria-describedby="aj-delivery-h" onChange={(e) => onChange({ ...value, key: e.target.value })}>
              {plain.map((o) => <option key={o.key} value={o.key}>{deliveryOptionLabel(o, trigger, t)}</option>)}
              {groups.map((g) => (
                <optgroup key={g.name} label={g.name}>
                  {g.options.map((o) => <option key={o.key} value={o.key}>{deliveryOptionLabel(o, trigger, t)}</option>)}
                </optgroup>
              ))}
            </select>
          </span>
        </div>
        <div className="dk-field">
          <label className="dk-label" htmlFor="aj-delivery-mode">{t('jobs.edit.delivery.mode')}</label>
          <span className="dk-select-wrap">
            <select id="aj-delivery-mode" className="dk-select" value={value.mode} data-testid="aj-delivery-mode"
              onChange={(e) => onChange({ ...value, mode: e.target.value })}>
              {DELIVERY_MODES.map((m) => <option key={m} value={m}>{deliveryModeLabel(m, t)}</option>)}
            </select>
          </span>
        </div>
      </div>
      <p className="dk-hint" id="aj-delivery-h">
        {fromChat ? t('jobs.edit.delivery.fromChat') : t('jobs.edit.delivery.hint')}{' '}
        <Link className="ag-link" to={`${CONNECTIONS_PATH}?tab=messaging`}>{t('jobs.reports.connections')}</Link>
      </p>
      {error && <p className="dk-field-error" role="alert" data-testid="aj-delivery-error">{t('jobs.edit.delivery.loadFailed', { message: error.message || '' })}</p>}
      {unusable.length > 0 && (
        <ul className="dk-hint aj-delivery-unusable" data-testid="aj-delivery-unusable">
          {unusable.map((c) => (
            <li key={c.id}>{t('jobs.edit.delivery.unusable', { name: c.service ? `${c.name} (${c.service})` : c.name, reason: t(`jobs.edit.delivery.reason.${c.reason}`) })}</li>
          ))}
        </ul>
      )}
    </div>
  )
}
