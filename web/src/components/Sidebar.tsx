import { NavLink } from 'react-router-dom'
import {
  LayoutDashboard,
  Route,
  PlayCircle,
  BarChart3,
  AlertCircle,
  FlaskConical,
  History,
  Settings,
} from 'lucide-react'

const items = [
  { path: '/', label: 'Overview', icon: LayoutDashboard },
  { path: '/routes', label: 'Routes', icon: Route },
  { path: '/runs', label: 'Runs', icon: PlayCircle },
  { path: '/analytics', label: 'Analytics', icon: BarChart3 },
  { path: '/errors', label: 'Errors', icon: AlertCircle },
  { path: '/backtest', label: 'Backtest', icon: FlaskConical },
  { path: '/history', label: 'History', icon: History },
  { path: '/settings', label: 'Settings', icon: Settings },
]

export function Sidebar() {
  return (
    <aside className="sidebar">
      <div style={{ padding: '16px', fontWeight: 700, fontSize: '1.25rem' }}>YuHaul</div>
      <nav aria-label="Main navigation">
        <ul className="nav-list">
          {items.map((item) => (
            <li key={item.path}>
              <NavLink to={item.path} className="nav-link" end={item.path === '/'}>
                <item.icon size={20} aria-hidden="true" />
                {item.label}
              </NavLink>
            </li>
          ))}
        </ul>
      </nav>
    </aside>
  )
}
