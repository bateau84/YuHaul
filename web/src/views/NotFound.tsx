import { Link } from 'react-router-dom'

export function NotFound() {
  return (
    <div className="card empty-state">
      <h1 className="page-title">Page not found</h1>
      <p>This view does not exist.</p>
      <Link to="/" className="button button--primary" style={{ marginTop: 16 }}>
        Go to Overview
      </Link>
    </div>
  )
}
