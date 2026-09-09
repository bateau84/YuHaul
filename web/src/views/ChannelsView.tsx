import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import * as api from '../api/client'
import { useToast } from '../components/ToastProvider'
import type { ChannelStats, ChannelDetail } from '../types'

function AdvisoryCard({ advisory }: { advisory: NonNullable<ChannelDetail['advisories']>[number] }) {
  const { addToast } = useToast()
  const labels: Record<string, string> = {
    low_usefulness: 'Low usefulness',
    declining_yield: 'Declining yield',
    high_duplicate_rate: 'High duplicate rate',
  }

  async function act(action: 'dismiss' | 'ignore' | 'act') {
    if (action === 'dismiss') await api.dismissAdvisory(advisory.id)
    else if (action === 'ignore') await api.ignoreAdvisory(advisory.id)
    else await api.actOnAdvisory(advisory.id, 'Reviewed channel')
    addToast('Advisory updated', 'success')
  }

  return (
    <div className="card" style={{ marginBottom: 12 }}>
      <strong>{labels[advisory.advisory_type] ?? advisory.advisory_type}</strong>
      <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>Generated {new Date(advisory.generated_at).toLocaleDateString()}</p>
      <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
        <button className="button" onClick={() => act('act')}>Take action</button>
        <button className="button" onClick={() => act('dismiss')}>Dismiss</button>
        <button className="button" onClick={() => act('ignore')}>Ignore 30 days</button>
      </div>
    </div>
  )
}

function ChannelDetailView({ channelId, onBack }: { channelId: string; onBack: () => void }) {
  const [channel, setChannel] = useState<ChannelDetail | null>(null)

  useEffect(() => {
    api.getChannel(channelId).then((c) => setChannel(c ?? null))
  }, [channelId])

  if (!channel) return <div className="empty-state">Loading channel...</div>

  return (
    <>
      <button className="button" onClick={onBack} style={{ marginBottom: 16 }}>
        Back to channels
      </button>
      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">{channel.channel_name}</h2>
        <p style={{ color: 'var(--color-text-muted)' }}>
          Retained: {channel.total_retained} ({channel.retention_signals?.user_driven ?? 0} user-driven,{' '}
          {channel.retention_signals?.yuhaul_routed ?? 0} YuHaul-routed — not counted as retention)
        </p>
        <div className="card-grid" style={{ marginTop: 16, marginBottom: 0 }}>
          <div className="card">
            <div className="metric-value">{channel.total_discovered}</div>
            <div className="metric-label">Discovered</div>
          </div>
          <div className="card">
            <div className="metric-value">{channel.total_routed}</div>
            <div className="metric-label">Routed</div>
          </div>
          <div className="card">
            <div className="metric-value">{(channel.usefulness_rate * 100).toFixed(1)}%</div>
            <div className="metric-label">Usefulness</div>
          </div>
          <div className="card">
            <div className="metric-value">{(channel.yield * 100).toFixed(1)}%</div>
            <div className="metric-label">Yield</div>
          </div>
        </div>
      </div>

      <h2 className="section-title">Discovery & Relevance Credit</h2>
      <div className="card" style={{ marginBottom: 16 }}>
        <p>
          Discovery credit: {channel.duplicate_provenance.discovery_credit} | Relevance credit:{' '}
          {channel.duplicate_provenance.relevance_credit}
        </p>
      </div>

      <h2 className="section-title">Advisories</h2>
      {(channel.advisories?.length ?? 0) === 0 ? (
        <div className="card empty-state">No advisories for this channel.</div>
      ) : (
        channel.advisories!.map((a) => <AdvisoryCard key={a.id} advisory={a} />)
      )}
    </>
  )
}

export function ChannelsView() {
  const { channelId } = useParams<{ channelId?: string }>()
  const [channels, setChannels] = useState<ChannelStats[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api.listChannels().then((c) => {
      setChannels(c)
      setLoading(false)
    })
  }, [])

  if (channelId) {
    return <ChannelDetailView channelId={channelId} onBack={() => window.history.back()} />
  }

  if (loading) return <div className="empty-state">Loading channels...</div>

  return (
    <>
      <div className="page-header">
        <h1 className="page-title">Channel Analysis</h1>
      </div>
      {channels.length === 0 ? (
        <div className="card empty-state">No channel data yet.</div>
      ) : (
        <div className="card">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Channel</th>
                <th scope="col">Discovered</th>
                <th scope="col">Routed</th>
                <th scope="col">Retained</th>
                <th scope="col">Usefulness</th>
              </tr>
            </thead>
            <tbody>
              {channels.map((c) => (
                <tr key={c.channel_id}>
                  <td data-label="Channel">
                    <Link to={`/analytics/channels/${c.channel_id}`}>{c.channel_name}</Link>
                    {c.advisories && c.advisories.length > 0 && (
                      <span className="badge badge--not-evaluated" style={{ marginLeft: 8 }}>
                        {c.advisories.length} advisory{c.advisories.length === 1 ? '' : 'ies'}
                      </span>
                    )}
                  </td>
                  <td data-label="Discovered">{c.total_discovered}</td>
                  <td data-label="Routed">{c.total_routed}</td>
                  <td data-label="Retained">{c.total_retained}</td>
                  <td data-label="Usefulness">{(c.usefulness_rate * 100).toFixed(1)}%</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  )
}
