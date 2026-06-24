// Content-shaped shimmer placeholders. Render `count` rows.
export default function Skeleton({ variant = 'line', width, height, count = 1, className = '' }) {
  const items = Array.from({ length: count })
  return (
    <>
      {items.map((_, i) => (
        <span
          key={i}
          className={`skeleton skeleton--${variant} ${className}`.trim()}
          style={{ width, height }}
          aria-hidden="true"
        />
      ))}
    </>
  )
}
