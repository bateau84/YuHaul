import { useState } from 'react'
import { NavLink } from 'react-router-dom'
import {
  LayoutDashboard,
  Route,
  PlayCircle,
  BarChart3,
  MoreHorizontal,
  AlertCircle,
  FlaskConical,
  History,
  Settings,
} from 'lucide-react'

const primary = [
  { path: '/', label: 'Overview', icon: LayoutDashboard },
  { path: '/routes', label: 'Routes', icon: Route },
  { path: '/runs', label: 'Runs', icon: PlayCircle },
  { path: '/analytics', label: 'Analytics', icon: BarChart3 },
]

const overflow = [
  { path: '/errors', label: 'Errors', icon: AlertCircle },
  { path: '/backtest', label: 'Backtest', icon: FlaskConical },
  { path: '/history', label: 'History', icon: History },
  { path: '/settings', label: 'Settings', icon: Settings },
]

export function BottomNav() {
  const [open, setOpen] = useState(false)

  return (
    <>
      <nav className="bottom-nav" aria-label="Main navigation">
        <ul className="bottom-nav-list">
          {primary.map((item) => (
            <li key={item.path}>
              <NavLink to={item.path} className="bottom-nav-link" end={item.path === '/'}>
                <item.icon size={20} aria-hidden="true" />
                {item.label}
              </NavLink>
            </li>
          ))}
          <li>
            <button
              className="bottom-nav-link"
              aria-expanded={open}
              aria-haspopup="menu"
              onClick={() => setOpen((v) => !v)}
            >
              <MoreHorizontal size={20} aria-hidden="true" />
              More
            </button>
          </li>
        </ul>
      </nav>
      {open && (
        <div
          style={{
            position: 'fixed',
            bottom: 72,
            left: 8,
            right: 8,
            background: 'var(--color-surface-elevated)',
            border: '1px solid var(--color-border-default)',
            borderRadius: 'var(--radius)',
            boxShadow: 'var(--shadow)',
            zIndex: 60,
            padding: 8,
          }}
          role="menu"
        >
          <ul style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gridTemplateColumns: 'repeat(2, 1fr)', gap: 4 }}>
            {overflow.map((item) => (
              <li key={item.path}>
                <NavLink
                  to={item.path}
                  className="nav-link"
                  onClick={() => setOpen(false)}
                  role="menuitem"
                >
                  <item.icon size={18} aria-hidden="true" />
                  {item.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      )}
    </>
  )
}
