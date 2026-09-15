// Mock API implementation for development and UI verification.
// Toggle to false to call the real backend endpoints (requires a running server).
export const USE_MOCK = import.meta.env.VITE_USE_MOCK !== 'false'

import type {
  Route,
  Run,
  VideoOutcome,
  DecisionDetail,
  ChannelStats,
  ChannelDetail,
  TrendBucket,
  RouteMatchRate,
  ConfigVersion,
  ConfigDiffResult,
  ConfigRouteChange,
  BacktestComparison,
  Settings,
  ErrorEntry,
  DeferredItem,
  Advisory,
  RetentionPlaylist,
} from '../types'

let mockRoutes: Route[] = [
  {
    id: 1,
    name: 'Music Discovery',
    description: 'Route music-related uploads to the discovery playlist.',
    evaluation_order: 1,
    is_final: true,
    is_enabled: true,
    condition_composition: 'AND',
    conditions: [
      { id: 101, route_id: 1, condition_index: 0, field: 'title', operator: 'contains', value: 'music' },
      { id: 102, route_id: 1, condition_index: 1, field: 'duration_seconds', operator: 'gt', value: '180' },
    ],
    actions: [{ id: 201, route_id: 1, action_type: 'playlist_insert', target_playlist_id: 'PLmusic', action_index: 0 }],
    exclusions: [{ id: 301, route_id: 1, exclusion_type: 'channel', exclusion_value: 'UCexcluded' }],
    created_at: '2026-08-01T10:00:00Z',
    updated_at: '2026-09-08T10:00:00Z',
  },
  {
    id: 2,
    name: 'Ambient',
    description: 'Long-form ambient and atmospheric content.',
    evaluation_order: 2,
    is_final: false,
    is_enabled: false,
    condition_composition: 'OR',
    conditions: [
      { id: 103, route_id: 2, condition_index: 0, field: 'title', operator: 'contains', value: 'ambient' },
      { id: 104, route_id: 2, condition_index: 1, field: 'duration_seconds', operator: 'gt', value: '600' },
    ],
    actions: [{ id: 202, route_id: 2, action_type: 'playlist_insert', target_playlist_id: 'PLambient', action_index: 0 }],
    exclusions: [],
    created_at: '2026-08-02T10:00:00Z',
    updated_at: '2026-09-08T10:00:00Z',
  },
]

let mockRuns: Run[] = [
  {
    id: 1,
    started_at: '2026-09-08T06:00:00Z',
    completed_at: '2026-09-08T06:05:00Z',
    run_type: 'scheduled',
    status: 'completed',
    config_version_id: 1,
    videos_discovered: 42,
    videos_accepted: 3,
    videos_rejected: 23,
    videos_excluded: 4,
    videos_deferred: 12,
    videos_failed: 0,
    videos_not_yet_evaluated: 0,
    quota_cost: 89,
    duration_seconds: 305,
  },
  {
    id: 2,
    started_at: '2026-09-07T06:00:00Z',
    completed_at: '2026-09-07T06:04:00Z',
    run_type: 'manual',
    status: 'completed',
    config_version_id: 1,
    videos_discovered: 12,
    videos_accepted: 1,
    videos_rejected: 6,
    videos_excluded: 2,
    videos_deferred: 3,
    videos_failed: 0,
    videos_not_yet_evaluated: 0,
    quota_cost: 52,
    duration_seconds: 245,
  },
]

const mockVideos: Record<number, VideoOutcome[]> = {
  1: [
    {
      id: 1001,
      video_id: 'dQw4w9WgXcQ',
      channel_id: 'UCdemo1',
      title: 'Late Night Jazz Mix',
      channel_name: 'Jazz Club',
      duration_seconds: 184,
      processing_state: 'completed',
      final_outcome: 'accepted',
    },
    {
      id: 1002,
      video_id: 'abc123',
      channel_id: 'UCdemo2',
      title: 'Ambient Study Session',
      channel_name: 'Focus Sounds',
      duration_seconds: 620,
      processing_state: 'evaluated',
      final_outcome: 'rejected',
    },
    {
      id: 1003,
      video_id: 'xyz789',
      channel_id: 'UCdemo3',
      title: 'Pop Hit 2026',
      channel_name: 'Pop Central',
      duration_seconds: 210,
      processing_state: 'deferred',
      final_outcome: 'deferred',
      deferred_reason: 'quota',
    },
  ],
}

const mockDecisionDetails: Record<string, DecisionDetail> = {
  '1/1001': {
    video: mockVideos[1][0],
    processing_state: 'completed',
    final_outcome: 'accepted',
    route_decisions: [
      {
        id: 5001,
        route_id: 1,
        route_name: 'Music Discovery',
        evaluation_order: 1,
        decision: 'matched',
        condition_audits: [
          { id: 6001, condition_index: 0, field: 'title', operator: 'contains', expected_value: 'music', actual_value: 'Late Night Jazz Mix', matched: true },
          { id: 6002, condition_index: 1, field: 'duration_seconds', operator: 'gt', expected_value: '180', actual_value: '184', matched: true },
        ],
        actions: [{ id: 7001, action_type: 'playlist_insert', target_playlist_id: 'PLmusic', outcome: 'succeeded' }],
      },
      {
        id: 5002,
        route_id: 2,
        route_name: 'Ambient',
        evaluation_order: 2,
        decision: 'skipped',
        skip_reason: 'final_route_matched',
        condition_audits: [],
        actions: [],
      },
    ],
  },
  '1/1002': {
    video: mockVideos[1][1],
    processing_state: 'evaluated',
    final_outcome: 'rejected',
    route_decisions: [
      {
        id: 5011,
        route_id: 1,
        route_name: 'Music Discovery',
        evaluation_order: 1,
        decision: 'not_matched',
        rejection_reason: 'title does not contain "music"',
        condition_audits: [
          { id: 6011, condition_index: 0, field: 'title', operator: 'contains', expected_value: 'music', actual_value: 'Ambient Study Session', matched: false },
          { id: 6012, condition_index: 1, field: 'duration_seconds', operator: 'gt', expected_value: '180', actual_value: '620', matched: true },
        ],
        actions: [],
      },
      {
        id: 5012,
        route_id: 2,
        route_name: 'Ambient',
        evaluation_order: 2,
        decision: 'skipped',
        skip_reason: 'disabled',
        condition_audits: [],
        actions: [],
      },
    ],
  },
  '1/1003': {
    video: mockVideos[1][2],
    processing_state: 'deferred',
    final_outcome: 'deferred',
    route_decisions: [
      {
        id: 5021,
        route_id: 1,
        route_name: 'Music Discovery',
        evaluation_order: 1,
        decision: 'matched',
        condition_audits: [
          { id: 6021, condition_index: 0, field: 'title', operator: 'contains', expected_value: 'music', actual_value: 'Pop Hit 2026', matched: false },
        ],
        actions: [{ id: 7021, action_type: 'playlist_insert', target_playlist_id: 'PLmusic', outcome: 'deferred' }],
      },
    ],
  },
}

const mockChannels: ChannelDetail[] = [
  {
    channel_id: 'UCdemo1',
    channel_name: 'Jazz Club',
    total_discovered: 60,
    total_routed: 12,
    total_retained: 8,
    usefulness_rate: 0.133,
    yield: 0.2,
    duplicate_rate: 0.25,
    duplicate_provenance: { discovery_credit: 45, relevance_credit: 15 },
    retention_signals: { user_driven: 6, yuhaul_routed: 2 },
    recent_videos: [mockVideos[1][0]],
    recent_video_provenance: [
      { video_id: 'dQw4w9WgXcQ', title: 'Late Night Jazz Mix', duplicate_credit: 'discovery', retention_source: 'user_driven' },
    ],
    matched_routes: ['Music Discovery'],
    advisories: [
      {
        id: 1,
        channel_id: 'UCdemo1',
        channel_name: 'Jazz Club',
        advisory_type: 'low_usefulness',
        generated_at: '2026-09-08T00:00:00Z',
        status: 'active',
        evidence_snapshot: {
          window_days: 90,
          discovered: 60,
          routed: 12,
          retained: 8,
          threshold: 0.1,
        },
      },
    ],
  },
  {
    channel_id: 'UCdemo2',
    channel_name: 'Focus Sounds',
    total_discovered: 30,
    total_routed: 4,
    total_retained: 1,
    usefulness_rate: 0.033,
    yield: 0.133,
    duplicate_rate: 0.667,
    duplicate_provenance: { discovery_credit: 10, relevance_credit: 20 },
    retention_signals: { user_driven: 1, yuhaul_routed: 0 },
    recent_videos: [mockVideos[1][1]],
    recent_video_provenance: [
      { video_id: 'abc123', title: 'Ambient Study Session', duplicate_credit: 'relevance', retention_source: 'user_driven' },
    ],
    matched_routes: ['Ambient'],
    advisories: [
      {
        id: 2,
        channel_id: 'UCdemo2',
        channel_name: 'Focus Sounds',
        advisory_type: 'high_duplicate_rate',
        generated_at: '2026-09-07T00:00:00Z',
        status: 'active',
        evidence_snapshot: {
          window_days: 90,
          discovered: 30,
          duplicate_matches: 20,
          threshold: 0.5,
        },
      },
    ],
  },
]

const mockTrends: TrendBucket[] = Array.from({ length: 14 }).map((_, i) => ({
  bucket: `2026-08-${String(26 + i).padStart(2, '0')}T00:00:00Z`,
  discovered: 10 + i,
  accepted: 1 + (i % 3),
  rejected: 6 + (i % 2),
  deferred: 2 + (i % 2),
  failed: i % 5 === 0 ? 1 : 0,
}))

const mockRouteMatchRates: RouteMatchRate[] = Array.from({ length: 14 }).flatMap((_, i) => [
  {
    bucket: `2026-08-${String(26 + i).padStart(2, '0')}T00:00:00Z`,
    route_id: 1,
    route_name: 'Music Discovery',
    evaluated: 8 + i,
    matched: 1 + (i % 3),
    match_rate: (1 + (i % 3)) / (8 + i),
  },
  {
    bucket: `2026-08-${String(26 + i).padStart(2, '0')}T00:00:00Z`,
    route_id: 2,
    route_name: 'Ambient',
    evaluated: 4 + i,
    matched: i % 4 === 0 ? 1 : 0,
    match_rate: i % 4 === 0 ? 1 / (4 + i) : 0,
  },
])

const mockConfigVersions: ConfigVersion[] = [
  {
    id: 1,
    created_at: '2026-08-01T10:00:00Z',
    snapshot: { routes: mockRoutes, duplicate_threshold: 0.5, duplicate_behavior: 'skip' },
    change_description: 'Initial configuration',
  },
]

let mockSettings: Settings = {
  schedule: '0 6 * * *',
  quota_limit: 10000,
  duplicate_threshold: 0.5,
  duplicate_behavior: 'skip',
  account_connected: true,
  account_email: 'user@example.com',
}

const mockErrors: ErrorEntry[] = [
  {
    id: 1,
    video_id: 1002,
    video_title: 'Ambient Study Session',
    channel_name: 'Focus Sounds',
    run_id: 1,
    error_category: 'authorization',
    error_message: 'OAuth token expired during playlist insertion.',
    status: 'active',
    created_at: '2026-09-08T06:04:00Z',
    retry_count: 0,
  },
]

const mockDeferred: DeferredItem[] = [
  {
    id: 1,
    video_id: 1003,
    video_title: 'Pop Hit 2026',
    channel_name: 'Pop Central',
    run_id: 1,
    deferral_reason: 'quota_exhausted',
    retry_after: '2026-09-09T06:00:00Z',
    status: 'pending',
    created_at: '2026-09-08T06:05:00Z',
    retry_count: 0,
  },
]

let mockRetentionPlaylists: RetentionPlaylist[] = [
  { id: 1, playlist_id: 'PLrotation', playlist_name: 'Rotation', designated_at: '2026-08-01T10:00:00Z', is_active: true },
]

let nextId = 10000

function uid(): number {
  return nextId++
}

function delay(ms = 300): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

export async function getOverview() {
  await delay()
  const lastRun = mockRuns[0]
  return {
    last_run: lastRun,
    runs_today: mockRuns.length,
    quota: {
      daily_limit: mockSettings.quota_limit,
      consumed_today: 1245,
      remaining: mockSettings.quota_limit - 1245,
      reset_at: '2026-09-09T00:00:00Z',
    },
    error_count: mockErrors.filter((e) => e.status === 'active').length,
    deferred_count: mockDeferred.filter((d) => d.status === 'pending').length,
  }
}

export async function listRoutes(): Promise<Route[]> {
  await delay()
  return [...mockRoutes].sort((a, b) => a.evaluation_order - b.evaluation_order)
}

export async function getRoute(id: number): Promise<Route | undefined> {
  await delay()
  return mockRoutes.find((r) => r.id === id)
}

export async function createRoute(route: Omit<Route, 'id' | 'created_at' | 'updated_at'>): Promise<Route> {
  await delay()
  const now = new Date().toISOString()
  const created: Route = { ...route, id: uid(), created_at: now, updated_at: now }
  mockRoutes.push(created)
  return created
}

export async function updateRoute(id: number, route: Omit<Route, 'id' | 'created_at' | 'updated_at'>): Promise<Route> {
  await delay()
  const idx = mockRoutes.findIndex((r) => r.id === id)
  if (idx === -1) throw new Error('route not found')
  const now = new Date().toISOString()
  mockRoutes[idx] = { ...route, id, created_at: mockRoutes[idx].created_at, updated_at: now }
  return mockRoutes[idx]
}

export async function deleteRoute(id: number): Promise<void> {
  await delay()
  mockRoutes = mockRoutes.filter((r) => r.id !== id)
}

export async function reorderRoutes(ids: number[]): Promise<void> {
  await delay()
  const map = new Map(mockRoutes.map((r) => [r.id, r]))
  mockRoutes = ids.map((id, i) => {
    const r = map.get(id)
    if (!r) throw new Error('route not found')
    r.evaluation_order = i + 1
    return r
  })
}

export async function listRuns(): Promise<Run[]> {
  await delay()
  return [...mockRuns].sort((a, b) => +new Date(b.started_at) - +new Date(a.started_at))
}

export async function getRun(id: number): Promise<Run | undefined> {
  await delay()
  return mockRuns.find((r) => r.id === id)
}

export async function getRunVideos(id: number): Promise<VideoOutcome[]> {
  await delay()
  return mockVideos[id] ?? []
}

export async function getDecisionDetail(runId: number, videoId: number): Promise<DecisionDetail | undefined> {
  await delay()
  return mockDecisionDetails[`${runId}/${videoId}`]
}

export async function startRun(): Promise<{ run_id: number; status: string }> {
  await delay()
  const id = uid()
  const run: Run = {
    id,
    started_at: new Date().toISOString(),
    run_type: 'manual',
    status: 'running',
    config_version_id: 1,
    videos_discovered: 0,
    videos_accepted: 0,
    videos_rejected: 0,
    videos_excluded: 0,
    videos_deferred: 0,
    videos_failed: 0,
    videos_not_yet_evaluated: 0,
    quota_cost: 0,
    current_phase: 'discovery',
  }
  mockRuns.unshift(run)
  return { run_id: id, status: 'running' }
}

export async function startDryRun(_draft?: { routes: Route[]; duplicate_threshold: number }): Promise<{ run_id: number; status: string }> {
  await delay()
  const id = uid()
  const run: Run = {
    id,
    started_at: new Date().toISOString(),
    run_type: 'dry_run',
    status: 'running',
    config_version_id: 1,
    videos_discovered: 0,
    videos_accepted: 0,
    videos_rejected: 0,
    videos_excluded: 0,
    videos_deferred: 0,
    videos_failed: 0,
    videos_not_yet_evaluated: 0,
    quota_cost: 0,
    current_phase: 'discovery',
  }
  mockRuns.unshift(run)
  return { run_id: id, status: 'running' }
}

export async function startReplay(_configVersionId?: number): Promise<{ run_id: number; status: string }> {
  return startDryRun()
}

export async function startBacktest(proposed: { routes: Route[]; duplicate_threshold: number }): Promise<{ run_id: number; status: string; comparison: BacktestComparison }> {
  await delay()
  const id = uid()
  // Simulate a completed backtest against the mock historical runs.
  const baselineAccepted = mockRuns
    .filter((r) => r.run_type === 'scheduled' || r.run_type === 'manual')
    .reduce((sum, r) => sum + r.videos_accepted, 0)
  const proposedAccepted = baselineAccepted + Math.floor(proposed.routes.length * 1.5)
  const newCatches = Math.max(0, proposedAccepted - baselineAccepted)
  const lostCatches = 0
  const comparison: BacktestComparison = {
    baseline_run_ids: mockRuns.map((r) => r.id),
    baseline_accepted: baselineAccepted,
    proposed_accepted: proposedAccepted,
    new_catches: newCatches,
    lost_catches: lostCatches,
    net_change: newCatches - lostCatches,
  }
  const run: Run = {
    id,
    started_at: new Date().toISOString(),
    completed_at: new Date().toISOString(),
    run_type: 'backtest',
    status: 'completed',
    config_version_id: 1,
    videos_discovered: 42,
    videos_accepted: proposedAccepted,
    videos_rejected: 23,
    videos_excluded: 4,
    videos_deferred: 0,
    videos_failed: 0,
    videos_not_yet_evaluated: 0,
    quota_cost: 0,
    duration_seconds: 120,
  }
  mockRuns.unshift(run)
  return { run_id: id, status: 'completed', comparison }
}

export async function listChannels(): Promise<ChannelStats[]> {
  await delay()
  return mockChannels.map((c) => ({ ...c }))
}

export async function getChannel(id: string): Promise<ChannelDetail | undefined> {
  await delay()
  return mockChannels.find((c) => c.channel_id === id)
}

export async function getTrends(
  from?: string,
  to?: string,
  granularity = 'day'
): Promise<{ from: string; to: string; granularity: string; buckets: TrendBucket[] }> {
  await delay()
  return { from: from ?? '', to: to ?? '', granularity, buckets: mockTrends }
}

export async function getRouteMatchRates(
  from?: string,
  to?: string,
  granularity = 'day'
): Promise<{ from: string; to: string; granularity: string; routes: RouteMatchRate[] }> {
  await delay()
  return { from: from ?? '', to: to ?? '', granularity, routes: mockRouteMatchRates }
}

export async function getQuota() {
  await delay()
  return {
    budget: {
      daily_limit: mockSettings.quota_limit,
      consumed_today: 1245,
      remaining: mockSettings.quota_limit - 1245,
      reset_at: '2026-09-09T00:00:00Z',
    },
    breakdown: [
      { operation_type: 'videos_list', units_consumed: 50, call_count: 50 },
      { operation_type: 'playlist_items_insert', units_consumed: 150, call_count: 3 },
      { operation_type: 'playlist_items_list', units_consumed: 45, call_count: 45 },
    ],
  }
}

export async function listErrors(include = 'active'): Promise<ErrorEntry[]> {
  await delay()
  return include === 'all' ? mockErrors : mockErrors.filter((e) => e.status === 'active')
}

export async function retryError(id: number): Promise<ErrorEntry | undefined> {
  await delay()
  const e = mockErrors.find((x) => x.id === id)
  if (e) e.status = 'retrying'
  return e
}

export async function dismissError(id: number): Promise<ErrorEntry | undefined> {
  await delay()
  const e = mockErrors.find((x) => x.id === id)
  if (e) e.status = 'dismissed'
  return e
}

export async function listDeferred(include = 'pending'): Promise<DeferredItem[]> {
  await delay()
  return include === 'all' ? mockDeferred : mockDeferred.filter((d) => d.status === 'pending')
}

export async function retryDeferred(id: number): Promise<DeferredItem | undefined> {
  await delay()
  const d = mockDeferred.find((x) => x.id === id)
  if (d) d.status = 'retrying'
  return d
}

export async function discardDeferred(id: number): Promise<DeferredItem | undefined> {
  await delay()
  const d = mockDeferred.find((x) => x.id === id)
  if (d) d.status = 'discarded'
  return d
}

export async function listConfigVersions(): Promise<ConfigVersion[]> {
  await delay()
  return [...mockConfigVersions]
}

export async function getConfigVersion(id: number): Promise<ConfigVersion | undefined> {
  await delay()
  return mockConfigVersions.find((v) => v.id === id)
}

export async function diffConfigVersion(id: number, compareTo?: number): Promise<ConfigDiffResult> {
  await delay()
  const to = mockConfigVersions.find((v) => v.id === id)
  if (!to) throw new Error('config version not found')
  const fromId = compareTo ?? (id > 1 ? id - 1 : id)
  const from = mockConfigVersions.find((v) => v.id === fromId) ?? to
  const toRouteIds = new Set(to.snapshot.routes.map((r) => r.id))
  const fromRouteIds = new Set(from.snapshot.routes.map((r) => r.id))
  const routesAdded = to.snapshot.routes.filter((r) => !fromRouteIds.has(r.id))
  const routesRemoved = from.snapshot.routes.filter((r) => !toRouteIds.has(r.id))
  const routesChanged: ConfigRouteChange[] = []
  for (const newRoute of to.snapshot.routes) {
    const oldRoute = from.snapshot.routes.find((r) => r.id === newRoute.id)
    if (!oldRoute || oldRoute.name === newRoute.name) continue
    const changes: { field: string; old: string; new: string }[] = []
    if (oldRoute.name !== newRoute.name) changes.push({ field: 'name', old: oldRoute.name, new: newRoute.name })
    if (oldRoute.is_enabled !== newRoute.is_enabled) {
      changes.push({ field: 'is_enabled', old: String(oldRoute.is_enabled), new: String(newRoute.is_enabled) })
    }
    routesChanged.push({ id: newRoute.id, old: oldRoute, new: newRoute, changes })
  }
  const settingsChanged: Record<string, { old: string; new: string }> = {}
  if (from.snapshot.duplicate_threshold !== to.snapshot.duplicate_threshold) {
    settingsChanged.duplicate_threshold = {
      old: String(from.snapshot.duplicate_threshold),
      new: String(to.snapshot.duplicate_threshold),
    }
  }
  return {
    from_id: from.id,
    to_id: to.id,
    settings_changed: settingsChanged,
    routes_added: routesAdded,
    routes_removed: routesRemoved,
    routes_changed: routesChanged,
  }
}

export async function restoreConfigVersion(id: number, description?: string): Promise<ConfigVersion> {
  await delay()
  const source = mockConfigVersions.find((v) => v.id === id)
  if (!source) throw new Error('config version not found')
  const restored: ConfigVersion = {
    id: uid(),
    created_at: new Date().toISOString(),
    snapshot: JSON.parse(JSON.stringify(source.snapshot)),
    change_description: description ?? `Restored from version ${id}`,
  }
  mockConfigVersions.push(restored)
  mockRoutes = restored.snapshot.routes.map((r) => JSON.parse(JSON.stringify(r)))
  return restored
}

export async function getSettings(): Promise<Settings> {
  await delay()
  return { ...mockSettings }
}

export async function updateSettings(settings: Partial<Settings>): Promise<Settings> {
  await delay()
  mockSettings = { ...mockSettings, ...settings }
  return { ...mockSettings }
}

export async function getDuplicateThreshold() {
  await delay()
  return { threshold: mockSettings.duplicate_threshold, default_threshold: 0.5, range: { min: 0.0, max: 1.0 } }
}

export async function setDuplicateThreshold(threshold: number): Promise<void> {
  await delay()
  mockSettings.duplicate_threshold = threshold
}

export async function listRetentionPlaylists(): Promise<RetentionPlaylist[]> {
  await delay()
  return [...mockRetentionPlaylists]
}

export async function createRetentionPlaylist(playlist: { playlist_id: string; playlist_name: string }): Promise<RetentionPlaylist> {
  await delay()
  const created: RetentionPlaylist = {
    id: uid(),
    playlist_id: playlist.playlist_id,
    playlist_name: playlist.playlist_name,
    designated_at: new Date().toISOString(),
    is_active: true,
  }
  mockRetentionPlaylists.push(created)
  return created
}

export async function deleteRetentionPlaylist(id: number): Promise<void> {
  await delay()
  mockRetentionPlaylists = mockRetentionPlaylists.filter((p) => p.id !== id)
}

export async function listAdvisories(): Promise<Advisory[]> {
  await delay()
  return mockChannels.flatMap((c) => c.advisories ?? [])
}

export async function dismissAdvisory(id: number): Promise<void> {
  await delay()
  for (const c of mockChannels) {
    const a = c.advisories?.find((x) => x.id === id)
    if (a) a.status = 'dismissed'
  }
}

export async function ignoreAdvisory(id: number): Promise<void> {
  await delay()
  for (const c of mockChannels) {
    const a = c.advisories?.find((x) => x.id === id)
    if (a) {
      a.status = 'ignored'
      a.ignored_until = new Date(Date.now() + 30 * 24 * 60 * 60 * 1000).toISOString()
    }
  }
}

export async function actOnAdvisory(id: number, action: string): Promise<void> {
  await delay()
  for (const c of mockChannels) {
    const a = c.advisories?.find((x) => x.id === id)
    if (a) {
      a.status = 'acted_upon'
      a.action_taken = action
    }
  }
}

export async function getAccountStatus() {
  await delay()
  return { connected: mockSettings.account_connected, email: mockSettings.account_email }
}

export async function disconnectAccount(): Promise<void> {
  await delay()
  mockSettings.account_connected = false
  mockSettings.account_email = undefined
}

export type SSEHandler = (event: { type: string; data: unknown }) => void

export function subscribeRunSSE(runId: number, handlers: {
  onEvent?: SSEHandler
  onError?: (error: Event) => void
  onOpen?: () => void
}): () => void {
  if (USE_MOCK) {
    const phases = ['discovery', 'duplicate_detection', 'routing', 'action_execution']
    let phase = 0
    handlers.onOpen?.()
    const interval = setInterval(() => {
      if (phase < phases.length) {
        handlers.onEvent?.({ type: 'run.phase', data: { run_id: runId, phase: phases[phase] } })
        phase++
      } else {
        handlers.onEvent?.({ type: 'run.completed', data: { run_id: runId, summary: { accepted: 3, rejected: 2, deferred: 0, failed: 0 } } })
        clearInterval(interval)
      }
    }, 1200)
    return () => clearInterval(interval)
  }

  const es = new EventSource(`/api/runs/${runId}/stream`)
  es.onopen = () => handlers.onOpen?.()
  es.onmessage = (e) => {
    try {
      const parsed = JSON.parse(e.data)
      handlers.onEvent?.({ type: parsed.type ?? 'unknown', data: parsed })
    } catch {
      handlers.onEvent?.({ type: 'raw', data: e.data })
    }
  }
  es.onerror = (e) => handlers.onError?.(e)
  return () => es.close()
}
