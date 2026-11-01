// Content-shaped placeholders. A light sweep crosses each block (transform
// only, 1.4 s); under reduced motion the blocks stay still.
//
//   variant   line | title | block | circle
//   count     how many to draw
//   width, height   to size one block to what it stands for
//
// Show skeletons for a first load only, in the shape of the real content,
// and not before 200 ms (SkeletonRows waits for you). A refresh keeps the old
// content on screen instead.
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

// Rows in the shape of a list row: a 36 px tile, a title line and a shorter
// description line. The group fades in after 200 ms, so a fast answer never
// flashes it.
export function SkeletonRows({ count = 3, label = 'Loading', className = '', testId }) {
  return (
    <div className={`skeleton-rows ${className}`.trim()} role="status" aria-label={label} aria-busy="true" data-testid={testId}>
      {Array.from({ length: count }).map((_, i) => (
        <div className="skeleton-row" key={i} aria-hidden="true">
          <span className="skeleton skeleton--tile" />
          <span className="skeleton-row__lines">
            <span className="skeleton skeleton--line skeleton--w60" />
            <span className="skeleton skeleton--line skeleton--w40" />
          </span>
        </div>
      ))}
    </div>
  )
}
