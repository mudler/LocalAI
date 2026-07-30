import { useEffect, useRef } from 'react'
import { useNavigate } from 'react-router-dom'

export default function Explorer() {
  const navigate = useNavigate()

  return (
    <div style={{ minHeight: '100vh', background: 'var(--color-bg-primary)', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', padding: 'var(--spacing-xl)' }}>
      <h1 style={{ fontSize: '2rem', fontWeight: 700, marginBottom: 'var(--spacing-md)' }}>
        <span className="text-primary">LocalAI Explorer</span>
      </h1>
      <p style={{ color: 'var(--color-text-secondary)', marginBottom: 'var(--spacing-xl)', textAlign: 'center' }}>
        Network visualization and node explorer
      </p>
      <div className="card" style={{ width: '100%', maxWidth: '800px', minHeight: '400px', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
        <div className="text-center text-muted">
          <i className="fas fa-network-wired" style={{ fontSize: '3rem', marginBottom: 'var(--spacing-md)' }} />
          <p>Explorer visualization</p>
        </div>
      </div>
      <button className="btn btn-secondary mt-lg" onClick={() => navigate('/app')}>
        <i className="fas fa-arrow-left" /> Back to Home
      </button>
    </div>
  )
}
