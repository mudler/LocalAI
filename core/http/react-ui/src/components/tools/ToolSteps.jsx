import Icon from '../Icon'
import './tools.css'

// The line above a tool that says where the job is: set up, check, run, result.
// `current` is the index of the step the person is on. A step before it is done.
// `failed` marks the current step as failed instead of in progress.
export default function ToolSteps({ steps, current, failed = false, label }) {
  return (
    <ol className="bt-steps" aria-label={label} data-testid="tool-steps">
      {steps.map((step, i) => {
        const state = i < current ? 'done' : i === current ? (failed ? 'failed' : 'current') : 'todo'
        return (
          <li key={step.key} className="bt-step" data-state={state} data-step={step.key} aria-current={i === current ? 'step' : undefined}>
            <span className="bt-step__mark" aria-hidden="true">
              {state === 'done' ? <Icon name="check" /> : state === 'failed' ? <Icon name="close" /> : i + 1}
            </span>
            <span className="bt-step__label">{step.label}</span>
          </li>
        )
      })}
    </ol>
  )
}
