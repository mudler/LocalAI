import { useNavigate } from 'react-router-dom'

export default function NotFound() {
  const navigate = useNavigate()

  return (
    <div className="page">
      <div className="empty-state">
        <div className="empty-state-icon"><i className="fas fa-compass" /></div>
        <h1 className="empty-state-title" style={{ fontSize: '3rem' }}>404</h1>
        <h2 className="empty-state-title">Page Not Found</h2>
        <p className="empty-state-text">The page you're looking for doesn't exist.</p>
        <button className="btn btn-primary" onClick={() => navigate('/')}>
          <i className="fas fa-home" /> Go Home
        </button>
      </div>
    </div>
  )
}
