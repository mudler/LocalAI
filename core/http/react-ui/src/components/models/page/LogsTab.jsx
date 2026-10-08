// eslint-disable-next-line no-unused-vars
import { BackendLogsPanel } from '../../../pages/BackendLogs'

// The backend's own output for this model, in the viewer the Operate section
// uses, without its page around it.
export default function LogsTab({ view }) {
  const { t, id } = view
  return (
    <div className="modelpage-logs" data-testid="model-page-logs">
      <div className="modelpage-section-head">
        <h2 className="modelpage-section-title">{t('page.tabs.logs')}</h2>
        <span className="modelpage-section-note">{t('page.logs.note')}</span>
      </div>
      <BackendLogsPanel modelId={id} fullPageLabel={t('page.logs.fullPage')} />
    </div>
  )
}
