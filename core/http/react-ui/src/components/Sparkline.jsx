// A bare trend line: stroke, no fill, no axes, no gridlines.
//
// It sits under a figure that already states the value, so its only job is to
// say what shape got us here. An emphasised endpoint marks "now", because the
// most recent point is the one being read.
//
// aria-hidden: the number above it is the accessible content, and a
// twelve-point series read aloud is noise.
export default function Sparkline({ points, tone = 'primary', width = 120, height = 28 }) {
  if (!Array.isArray(points) || points.length < 2) return null

  const max = Math.max(...points)
  // A flat run of zeroes would otherwise divide by zero and draw nothing;
  // pinning it to the baseline is the honest picture of "no traffic".
  const scale = max > 0 ? max : 1
  const step = width / (points.length - 1)
  const y = v => height - 2 - (v / scale) * (height - 4)

  const path = points.map((v, i) => `${(i * step).toFixed(1)},${y(v).toFixed(1)}`).join(' ')
  const lastX = width
  const lastY = y(points[points.length - 1])

  return (
    <svg
      className={`sparkline sparkline--${tone}`}
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      aria-hidden="true"
      focusable="false"
    >
      <polyline
        points={path}
        fill="none"
        strokeWidth="1.5"
        vectorEffect="non-scaling-stroke"
      />
      <circle cx={lastX} cy={lastY} r="2" />
    </svg>
  )
}
