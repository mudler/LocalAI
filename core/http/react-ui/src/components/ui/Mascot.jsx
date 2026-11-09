import welcome from '../../assets/mascot/welcome.webp'

// The LocalAI llama, for the places where the app has something friendly to
// say: a first run, an empty list, a page that is not there. It is a picture
// beside the words, never the only carrier of meaning, so it is hidden from
// assistive technology and has no animation.
//
//   pose   which picture. Only "welcome" exists today; any other pose falls
//          back to it, so a call can already name the pose it wants and the
//          art can arrive later (see POSES).
//   size   "sm" (72 px high), "md" (112 px, default) or "lg" (160 px)
//
// Do not put it on dense operator pages (nodes, traffic, traces, logs) or on
// a state where an error needs attention.
export const POSES = { welcome }

export default function Mascot({ pose = 'welcome', size = 'md', className = '' }) {
  const src = POSES[pose] || POSES.welcome
  return (
    <img
      className={`ui-mascot ui-mascot--${size} ${className}`.trim()}
      src={src}
      alt=""
      aria-hidden="true"
      draggable="false"
      decoding="async"
      data-pose={pose}
    />
  )
}
