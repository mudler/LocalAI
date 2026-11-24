export default function SettingRow({ label, description, children }) {
  return (
    <div className="form-row">
      <div className="form-row__label">
        <div className="form-row__label-text">{label}</div>
        {description && <div className="form-row__hint">{description}</div>}
      </div>
      <div className="form-row__control">{children}</div>
    </div>
  )
}
