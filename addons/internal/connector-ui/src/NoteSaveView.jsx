// How a note is saved on this connection, drawn from the access view of the
// host. It shows only when the agent of the connection has its own rule Save it
// and tell me, which the host reports as note_save on each kind of sender. The
// rule itself is chosen on the agent, never here.
export default function NoteSaveView({ subjects, s, testId = 'connector-notesave' }) {
  const rows = (Array.isArray(subjects) ? subjects : []).filter((x) => x && (x.note_save === 'now' || x.note_save === 'ask'))
  if (rows.length === 0) return null
  return (
    <div className="cn-approvals" data-testid={testId}>
      <h4 className="cn-approvals__title">{s('access.noteSave.title')}</h4>
      <ul className="cn-approvals__list" aria-label={s('access.noteSave.title')}>
        {rows.map((x) => (
          <li key={x.id} className="cn-approvals__row" data-testid={`${testId}-${x.id}`} data-state={x.note_save}>
            {rows.length > 1 && <span className="cn-muted">{x.title}</span>}
            <span>{s(`access.noteSave.${x.note_save}`)}</span>
          </li>
        ))}
      </ul>
    </div>
  )
}
