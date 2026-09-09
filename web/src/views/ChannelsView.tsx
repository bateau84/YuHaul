import { useEffect, useState } from 'react'
import { Link, useParams, useNavigate } from 'react-router-dom'
import { Check, AlertTriangle, TrendingDown, Copy, ExternalLink, Star, Pin, Sparkles, Repeat } from 'lucide-react'
import * as api from '../api/client'
import { useToast } from '../components/ToastProvider'
import type { ChannelStats, ChannelDetail, Advisory, VideoProvenance } from '../types'

const advisoryLabels: Record<Advisory['advisory_type'], string> = {
  low_usefulness: 'Low usefulness',
  declining_yield: 'Declining yield',
  high_duplicate_rate: 'High duplicate rate',
}

const advisoryIcons: Record<Advisory['advisory_type'], typeof AlertTriangle> = {
  low_usefulness: TrendingDown,
  declining_yield: TrendingDown,
  high_duplicate_rate: Copy,
}

function formatEvidence(a: Advisory): string {
  const e = a.evidence_snapshot
  if (!e) return 'No detailed evidence available.'
  if (a.advisory_type === 'low_usefulness' && e.discovered !== undefined && e.retained !== undefined) {
    const rate = e.discovered > 0 ? (e.retained / e.discovered) * 100 : 0
    return `${rate.toFixed(1)}% usefulness (${e.retained} retained of ${e.discovered} discovered) over ${e.window_days ?? 90} days.`
  }
  if (a.advisory_type === 'high_duplicate_rate' && e.discovered !== undefined && e.duplicate_matches !== undefined) {
    const rate = e.discovered > 0 ? (e.duplicate_matches / e.discovered) * 100 : 0
    return `${rate.toFixed(1)}% duplicate rate (${e.duplicate_matches} of ${e.discovered} discovered) over ${e.window_days ?? 90} days.`
  }
  if (a.advisory_type === 'declining_yield' && e.baseline_yield_rate !== undefined && e.current_yield_rate !== undefined) {
    return `Yield dropped from ${(e.baseline_yield_rate * 100).toFixed(1)}% to ${(e.current_yield_rate * 100).toFixed(1)}%.`
  }
  return 'See metrics below for supporting evidence.'
}

function AdvisoryBadge({ advisory }: { advisory: Advisory }) {
  const Icon = advisoryIcons[advisory.advisory_type]
  return (
    <span className={`badge badge--${advisory.advisory_type === 'low_usefulness' ? 'failed' : 'deferred'}`}>
      <Icon size={14} aria-hidden="true" />
      {advisoryLabels[advisory.advisory_type]}
    </span>
  )
}

function AdvisoryCard({ advisory, onChange }: { advisory: Advisory; onChange: () => void }) {
  const { addToast } = useToast()
  const [acting, setActing] = useState(false)

  async function act(action: 'dismiss' | 'ignore' | 'act') {
    setActing(true)
    try {
      if (action === 'dismiss') await api.dismissAdvisory(advisory.id)
      else if (action === 'ignore') await api.ignoreAdvisory(advisory.id)
      else await api.actOnAdvisory(advisory.id, 'Reviewed channel on YouTube')
      addToast('Advisory updated', 'success')
      onChange()
    } catch {
      addToast('Failed to update advisory', 'error')
    } finally {
      setActing(false)
    }
  }

  return (
    <div className="card" style={{ marginBottom: 12 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
        <AdvisoryBadge advisory={advisory} />
        <span style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem' }}>
          Generated {new Date(advisory.generated_at).toLocaleDateString()}
        </span>
      </div>
      <p style={{ margin: '0 0 8px' }}>{formatEvidence(advisory)}</p>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        <button className="button button--primary" onClick={() => act('act')} disabled={acting}>
          <ExternalLink size={16} aria-hidden="true" />
          Take action
        </button>
        <button className="button" onClick={() => act('dismiss')} disabled={acting}>
          <Check size={16} aria-hidden="true" />
          Dismiss
        </button>
        <button className="button" onClick={() => act('ignore')} disabled={acting}>
          Ignore 30 days
        </button>
      </div>
      {advisory.ignored_until && (
        <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem', marginTop: 8 }}>
          Ignored until {new Date(advisory.ignored_until).toLocaleDateString()}
        </p>
      )}
    </div>
  )
}

function ProvenanceBadge({ type, label }: { type: 'discovery' | 'relevance' | 'user_driven' | 'yuhaul_routed'; label: string }) {
  const variants: Record<typeof type, string> = {
    discovery: 'badge--accepted',
    relevance: 'badge--deferred',
    user_driven: 'badge--accepted',
    yuhaul_routed: 'badge--not-evaluated',
  }
  const icons = {
    discovery: Sparkles,
    relevance: Repeat,
    user_driven: Star,
    yuhaul_routed: Pin,
  }
  const Icon = icons[type]
  return (
    <span className={`badge ${variants[type]}`}>
      <Icon size={14} aria-hidden="true" />
      {label}
    </span>
  )
}

function VideoProvenanceRow({ video, provenance }: { video: { video_id: string; title?: string }; provenance?: VideoProvenance }) {
  return (
    <li style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 12, padding: '8px 0', borderBottom: '1px solid var(--color-border-default)' }}>
      <span>{video.title ?? video.video_id}</span>
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
        {provenance?.duplicate_credit === 'discovery' && <ProvenanceBadge type="discovery" label="First discovered" />}
        {provenance?.duplicate_credit === 'relevance' && <ProvenanceBadge type="relevance" label="Also published elsewhere" />}
        {provenance?.retention_source === 'user_driven' && <ProvenanceBadge type="user_driven" label="User-added" />}
        {provenance?.retention_source === 'yuhaul_routed' && <ProvenanceBadge type="yuhaul_routed" label="YuHaul-routed" />}
      </div>
    </li>
  )
}

function percentage(part: number, whole: number): string {
  return whole > 0 ? `${((part / whole) * 100).toFixed(1)}%` : '0.0%'
}

function ChannelDetailView({ channelId, onBack }: { channelId: string; onBack: () => void }) {
  const [channel, setChannel] = useState<ChannelDetail | null>(null)
  const [refresh, setRefresh] = useState(0)

  useEffect(() => {
    api.getChannel(channelId).then((c) => setChannel(c ?? null))
  }, [channelId, refresh])

  if (!channel) return <div className="empty-state">Loading channel...</div>

  const dup = channel.duplicate_provenance
  const ret = channel.retention_signals
  const totalDuplicates = dup.discovery_credit + dup.relevance_credit
  const totalRetention = ret.user_driven + ret.yuhaul_routed

  return (
    <>
      <button className="button" onClick={onBack} style={{ marginBottom: 16 }}>
        Back to channels
      </button>

      <div className="card" style={{ marginBottom: 16 }}>
        <h2 className="section-title">{channel.channel_name}</h2>
        <p style={{ color: 'var(--color-text-muted)' }}>
          Retained: {channel.total_retained} ({ret.user_driven} user-driven, {ret.yuhaul_routed} YuHaul-routed — not counted as retention)
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
        <div className="card-grid" style={{ marginBottom: 0 }}>
          <div className="card">
            <div className="metric-value">{dup.discovery_credit}</div>
            <div className="metric-label">Discovery credit ({percentage(dup.discovery_credit, totalDuplicates)})</div>
            <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem', marginTop: 4 }}>
              This channel was the first source to publish the content.
            </p>
          </div>
          <div className="card">
            <div className="metric-value">{dup.relevance_credit}</div>
            <div className="metric-label">Relevance credit ({percentage(dup.relevance_credit, totalDuplicates)})</div>
            <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem', marginTop: 4 }}>
              This channel confirmed relevance of content already discovered elsewhere.
            </p>
          </div>
        </div>
      </div>

      <h2 className="section-title">Retention Signal Provenance</h2>
      <div className="card" style={{ marginBottom: 16 }}>
        <div className="card-grid" style={{ marginBottom: 0 }}>
          <div className="card">
            <div className="metric-value">{ret.user_driven}</div>
            <div className="metric-label">User-driven retention ({percentage(ret.user_driven, totalRetention)})</div>
            <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem', marginTop: 4 }}>
              Added to a designated retention playlist by you or an external process.
            </p>
          </div>
          <div className="card">
            <div className="metric-value">{ret.yuhaul_routed}</div>
            <div className="metric-label">YuHaul-routed membership ({percentage(ret.yuhaul_routed, totalRetention)})</div>
            <p style={{ color: 'var(--color-text-muted)', fontSize: '0.875rem', marginTop: 4 }}>
              Added by YuHaul routing. Not counted as a retention signal.
            </p>
          </div>
        </div>
      </div>

      {channel.matched_routes.length > 0 && (
        <>
          <h2 className="section-title">Matched routes</h2>
          <div className="card" style={{ marginBottom: 16 }}>
            <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
              {channel.matched_routes.map((r) => (
                <li key={r} style={{ padding: '6px 0' }}>
                  {r}
                </li>
              ))}
            </ul>
          </div>
        </>
      )}

      {channel.recent_videos.length > 0 && (
        <>
          <h2 className="section-title">Recent videos</h2>
          <div className="card" style={{ marginBottom: 16 }}>
            <ul style={{ listStyle: 'none', padding: 0, margin: 0 }}>
              {channel.recent_videos.map((v) => (
                <VideoProvenanceRow
                  key={v.video_id}
                  video={v}
                  provenance={channel.recent_video_provenance?.find((p) => p.video_id === v.video_id)}
                />
              ))}
            </ul>
          </div>
        </>
      )}

      <h2 className="section-title">Advisories</h2>
      {(channel.advisories?.length ?? 0) === 0 ? (
        <div className="card empty-state">No advisories for this channel.</div>
      ) : (
        channel.advisories!.map((a) => <AdvisoryCard key={a.id} advisory={a} onChange={() => setRefresh((n) => n + 1)} />)
      )}
    </>
  )
}

export function ChannelsView() {
  const { channelId } = useParams<{ channelId?: string }>()
  const navigate = useNavigate()
  const [channels, setChannels] = useState<ChannelStats[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    api.listChannels().then((c) => {
      setChannels(c)
      setLoading(false)
    })
  }, [])

  if (channelId) {
    return <ChannelDetailView channelId={channelId} onBack={() => navigate('/analytics/channels')} />
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
                      <span style={{ display: 'flex', gap: 6, marginTop: 6, flexWrap: 'wrap' }}>
                        {c.advisories.map((a) => (
                          <AdvisoryBadge key={a.id} advisory={a} />
                        ))}
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
