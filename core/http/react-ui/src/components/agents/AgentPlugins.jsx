/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useTranslation } from 'react-i18next'
import { LoadError, SkeletonRows, useLoad } from '../ui'
import { SwitchRow } from '../chat/SwitchList'
import { pluginsApi } from '../../utils/pluginsClient'
import { pluginName } from '../../utils/chatPlugins'

// The plugins a chat with this agent may use: an exact grant (an empty list
// grants none), as for add-ons. A plugin contributes only when the person
// also turned it on for themselves. A granted install that is gone stays in
// the list until it is switched off, so the save does not drop it silently.
//
//   value      the install IDs the agent grants
//   onChange   (ids) => void; the change applies with the form's Save
export default function AgentPlugins({ value = [], onChange, api = pluginsApi }) {
  const { t } = useTranslation('agents')
  const list = useLoad(() => api.installs(), [])
  if (list.status === 'loading') return <SkeletonRows count={2} />
  if (list.status === 'error') return <LoadError title={t('create.pluginsLoadFailed')} error={list.error} onRetry={list.retry} />
  const installs = list.data || []
  const known = new Set(installs.map((i) => i.id))
  const gone = value.filter((id) => !known.has(id))
  const set = (id, on) => onChange(on ? [...new Set([...value, id])] : value.filter((x) => x !== id))
  return (
    <>
      <p className="ag-note">{t('create.pluginsHelp')}</p>
      {installs.length === 0 && gone.length === 0 ? (
        <p className="ag-note" data-testid="agent-plugins-none">{t('create.pluginsNone')}</p>
      ) : (
        <div className="ag-switches" data-testid="agent-plugins">
          {installs.map((i) => (
            <SwitchRow
              key={i.id}
              name={i.name || pluginName(i.plugin)}
              desc={i.enabled && !i.needsReenable ? i.plugin : t('create.pluginOffForYou', { plugin: i.plugin })}
              checked={value.includes(i.id)}
              onChange={(on) => set(i.id, on)}
              testId="agent-plugin-row"
              data={{ id: i.id }}
            />
          ))}
          {gone.map((id) => (
            <SwitchRow key={id} name={id} desc={t('create.pluginGone')} checked onChange={(on) => set(id, on)} testId="agent-plugin-row" data={{ id, gone: 'true' }} />
          ))}
        </div>
      )}
    </>
  )
}
