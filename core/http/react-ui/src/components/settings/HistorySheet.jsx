/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import SideSheet from '../SideSheet'
import { FIELD_BY_KEY } from '../../utils/settingsSchema'
import { displayValue } from '../../utils/settingsPending'
import { Button, EmptyState } from '../ui'

function when(at) {
  const d = new Date(at)
  const now = new Date()
  const time = d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
  if (d.toDateString() === now.toDateString()) return `Today ${time}`
  return `${d.toLocaleDateString([], { day: 'numeric', month: 'short' })} ${time}`
}

// The settings changed from this browser, newest first. LocalAI keeps no
// settings log of its own, so this is the browser's own list and it says so.
// Revert puts the old value back as an edit; nothing applies until Apply.
export default function HistorySheet({ entries, onRevert, onClear, onClose }) {
  return (
    <SideSheet
      title="Settings history"
      description="Changes applied from this browser. LocalAI keeps no settings log, so changes made elsewhere do not appear. Revert puts the old value back as an edit; nothing applies until you apply it."
      onClose={onClose} closeLabel="Close history" testId="settings-history" labelId="settings-history-title"
      footer={entries.length > 0 ? (
        <Button variant="ghost" onClick={onClear}>Clear this list</Button>
      ) : null}
    >
      {entries.length === 0 ? (
        <EmptyState
          variant="first-run"
          title="Nothing applied yet"
          level={3}
          body="Settings you apply from this browser are listed here, up to the last 50."
        />
      ) : (
        <ul className="st-history">
          {entries.map((e, i) => {
            const field = FIELD_BY_KEY[e.key]
            if (!field) return null
            const canRevert = !field.sensitive && e.from !== null
            return (
              <li key={`${e.at}-${e.key}-${i}`} className="st-history__row" data-testid="history-row">
                <div className="st-history__main">
                  <span className="st-history__label">{field.label}</span>
                  <span className="st-history__values dk-mono">
                    {field.sensitive ? 'changed' : <>{displayValue(field, e.from)} → {displayValue(field, e.to)}</>}
                  </span>
                </div>
                <div className="st-history__end">
                  <time className="st-history__time" dateTime={new Date(e.at).toISOString()}>{when(e.at)}</time>
                  {canRevert && <button type="button" className="st-reset" onClick={() => onRevert(e)}>Revert</button>}
                </div>
              </li>
            )
          })}
        </ul>
      )}
    </SideSheet>
  )
}
