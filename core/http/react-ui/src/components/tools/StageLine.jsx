import './tools.css'

// The stages a job passes through, as a row of segments. The current stage is
// named in bold; stages before it are filled. `stages` are status ids and
// `labels` maps each to its words.
export default function StageLine({ stages, labels, status, failed = false, label }) {
  const at = stages.indexOf(status)
  return (
    <ol className="bt-stages" aria-label={label} data-testid="job-stages">
      {stages.map((stage, i) => {
        const state = i < at ? 'done' : i === at ? (failed ? 'failed' : 'current') : 'todo'
        return (
          <li key={stage} className="bt-stage" data-state={state} aria-current={state === 'current' ? 'step' : undefined}>
            <span className="bt-stage__bar" aria-hidden="true" />
            <span className="bt-stage__label">{labels[stage]}</span>
          </li>
        )
      })}
    </ol>
  )
}
