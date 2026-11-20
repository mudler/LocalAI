// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars -- used in JSX; this config has no eslint-plugin-react
import { AppIcon } from '../addons/AddonBits'
import { CONNECTIONS_PATH } from '../../utils/connectionsTabs'
import { deliveryOf } from '../../utils/triggerDelivery'
import './reports-to.css'

// Where the runs of a trigger report: the Inbox, or a channel or chat of a
// connection (icon of the app, its name, the place). When the connection is off
// or gone, a warning with words says so and where the reports go instead.
//   compact  one line for a list row
//   (default) a block with the mode and the link to the connection
// icons is the add-on icon map of useAddonIcons; a missing icon draws a letter.
export default function ReportsTo({ trigger, icons = {}, compact = false }) {
  const { t } = useTranslation('agents')
  const d = deliveryOf(trigger)
  const problem = d.problem
  const where = d.kind === 'inbox'
    ? t('jobs.reports.inbox')
    : d.kind === 'channel'
      ? t('jobs.reports.channel', { channel: d.place })
      : t(d.thread ? 'jobs.reports.thread' : 'jobs.reports.chat', { chat: d.place })
  const app = d.connection && d.service ? `${d.connection} (${d.service})` : (d.connection || d.service)
  const title = [where, app].filter(Boolean).join(' - ')
  return (
    <span className={`rt${compact ? ' rt--compact' : ''}`} data-testid="reports-to" data-kind={d.kind} data-problem={problem || undefined}>
      <span className="rt__main" title={title}>
        {d.kind === 'inbox'
          ? <Icon name="inbox" />
          : <AppIcon src={icons[d.addon] || ''} id={d.addon} name={d.service || d.connection} size={compact ? 16 : 20} />}
        <span className="rt__text">
          {compact && <span className="sr-only">{t('jobs.reports.label')}</span>}
          <span className="rt__where">{where}</span>
          {app && <span className="rt__app">{app}</span>}
        </span>
      </span>
      {problem && (
        <span className="rt__warn" role="status" data-testid="reports-to-problem">
          <Icon name="warning" />
          <span>{t(`jobs.reports.problem.${problem}`)}</span>
        </span>
      )}
      {!compact && d.mode && <span className="rt__mode">{t(`jobs.reports.mode.${d.mode}`)}</span>}
      {!compact && problem && <span className="rt__note">{t('jobs.reports.fallback')}</span>}
      {!compact && d.kind !== 'inbox' && problem !== 'gone' && <Link className="ag-link rt__link" to={`${CONNECTIONS_PATH}?tab=messaging`}>{t('jobs.reports.manage')}</Link>}
      {!compact && problem === 'gone' && <Link className="ag-link rt__link" to={`${CONNECTIONS_PATH}?tab=messaging`}>{t('jobs.reports.connections')}</Link>}
    </span>
  )
}
