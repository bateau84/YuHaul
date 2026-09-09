import { Routes, Route, useLocation } from 'react-router-dom'
import { useEffect, useRef, useState } from 'react'
import { Sidebar } from './components/Sidebar'
import { BottomNav } from './components/BottomNav'
import { Overview } from './views/Overview'
import { RoutesView } from './views/RoutesView'
import { RouteEditorView } from './views/RouteEditorView'
import { RunsView } from './views/RunsView'
import { RunDetailView } from './views/RunDetailView'
import { DecisionDetailView } from './views/DecisionDetailView'
import { AnalyticsView } from './views/AnalyticsView'
import { ChannelsView } from './views/ChannelsView'
import { ErrorsView } from './views/ErrorsView'
import { BacktestView } from './views/BacktestView'
import { HistoryView } from './views/HistoryView'
import { SettingsView } from './views/SettingsView'
import { NotFound } from './views/NotFound'

function Layout({ children }: { children: React.ReactNode }) {
  const location = useLocation()
  const mainRef = useRef<HTMLDivElement>(null)
  const [smallScreen, setSmallScreen] = useState(false)

  useEffect(() => {
    const check = () => setSmallScreen(window.innerWidth < 320)
    check()
    window.addEventListener('resize', check)
    return () => window.removeEventListener('resize', check)
  }, [])

  useEffect(() => {
    const h1 = mainRef.current?.querySelector('h1') as HTMLElement | null
    h1?.focus()
  }, [location.pathname])

  return (
    <div className="layout">
      <a href="#main-content" className="skip-link">
        Skip to main content
      </a>
      <Sidebar />
      <BottomNav />
      <div className="main">
        {smallScreen && (
          <div className="small-warning" role="alert">
            Screen is very small. Some features may be limited. Consider using landscape orientation.
          </div>
        )}
        <main id="main-content" className="main-scroll" ref={mainRef} tabIndex={-1}>
          {children}
        </main>
      </div>
    </div>
  )
}

function AppRoutes() {
  return (
    <Routes>
      <Route path="/" element={<Overview />} />
      <Route path="/routes" element={<RoutesView />} />
      <Route path="/routes/new" element={<RouteEditorView />} />
      <Route path="/routes/:id" element={<RouteEditorView />} />
      <Route path="/runs" element={<RunsView />} />
      <Route path="/runs/:id" element={<RunDetailView />} />
      <Route path="/runs/:id/video/:videoId" element={<DecisionDetailView />} />
      <Route path="/analytics" element={<AnalyticsView />} />
      <Route path="/analytics/channels" element={<ChannelsView />} />
      <Route path="/analytics/channels/:channelId" element={<ChannelsView />} />
      <Route path="/errors" element={<ErrorsView />} />
      <Route path="/backtest" element={<BacktestView />} />
      <Route path="/history" element={<HistoryView />} />
      <Route path="/history/:versionId" element={<HistoryView />} />
      <Route path="/settings" element={<SettingsView />} />
      <Route path="*" element={<NotFound />} />
    </Routes>
  )
}

export default function App() {
  return (
    <Layout>
      <AppRoutes />
    </Layout>
  )
}
