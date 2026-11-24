export default function SectionHeading({ children, className = '' }) {
  return <h3 className={`section-heading ${className}`.trim()}>{children}</h3>
}
