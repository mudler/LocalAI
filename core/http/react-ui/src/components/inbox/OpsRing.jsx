// A circular progress ring, drawn on a 24 unit square and sized by CSS. It is
// a picture: the numbers it stands for are read from the rows, so it is hidden
// from assistive technology.
//
//   value    0 to 100, or null for work that has not started (an arc turns)
//   failed   draws it in the error colour
const R = 9
const C = 2 * Math.PI * R

export default function OpsRing({ value, failed = false, className = '' }) {
  const waiting = value == null
  const dash = waiting ? C * 0.25 : (C * Math.max(3, Math.min(100, value))) / 100
  const cls = ['ops-ring', waiting && 'ops-ring--wait', failed && 'ops-ring--failed', className].filter(Boolean).join(' ')
  return (
    <svg className={cls} viewBox="0 0 24 24" aria-hidden="true" focusable="false">
      <circle className="ops-ring__track" cx="12" cy="12" r={R} />
      <circle className="ops-ring__arc" cx="12" cy="12" r={R} strokeDasharray={`${dash} ${C}`} />
    </svg>
  )
}
