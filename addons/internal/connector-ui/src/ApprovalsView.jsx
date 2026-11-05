/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
// The tools of the add-on whose confirmation the owner can relax, and who they
// run for without asking right now. It draws what the host reports in the
// access view (saved or preview); the owner's choice itself is made on the
// settings page of the add-on, never here.
import { approvalState } from './approvals.js'

export default function ApprovalsView({ approvals, s, sender = '', testId = 'connector-approvals' }) {
  const list = Array.isArray(approvals) ? approvals : []
  if (list.length === 0) return null
  return (
    <div className="cn-approvals" data-testid={testId}>
      <h4 className="cn-approvals__title">{s('approvals.title')}</h4>
      <p className="dk-hint">{s('approvals.help')}</p>
      <ul className="cn-approvals__list" aria-label={s('approvals.title')}>
        {list.map((a) => {
          const state = approvalState(a, sender)
          return (
            <li key={a.tool} className="cn-approvals__row" data-testid={`connector-approval-${a.tool}`} data-state={state}>
              <code className="cn-approvals__tool">{a.tool}</code>
              <span className={`cn-approvals__state cn-approvals__state--${state}`}>{s(`approvals.state.${state}`)}</span>
              <span className="cn-muted">{s(`approvals.line.${state}`)}</span>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
