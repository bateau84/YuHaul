import '@testing-library/jest-dom'
import * as mock from '../api/mock'
import type { Route, Settings } from '../types'

const originalFetch = globalThis.fetch

function parsePath(url: string): { path: string; search: URLSearchParams } {
  const stripped = url.replace(/^https?:\/\/[^/]+/, '')
  const [rawPath, query] = stripped.split('?')
  const path = rawPath.length > 1 && rawPath.endsWith('/') ? rawPath.slice(0, -1) : rawPath
  return { path, search: new URLSearchParams(query ?? '') }
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function routeFromBody(body: string): Omit<Route, 'id' | 'created_at' | 'updated_at'> {
  const parsed = JSON.parse(body) as Route
  const { id: _id, created_at: _created, updated_at: _updated, ...rest } = parsed
  return rest
}

globalThis.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
  if (!url.startsWith('/api/') && !url.includes('/api/')) {
    return originalFetch(input, init)
  }

  const { path, search } = parsePath(url)
  const method = init?.method?.toUpperCase() ?? 'GET'

  // Analytics
  if (path === '/api/analytics/channels' && method === 'GET') {
    return jsonResponse({ channels: await mock.listChannels() })
  }
  const channelMatch = path.match(/^\/api\/analytics\/channels\/([^/]+)$/)
  if (channelMatch && method === 'GET') {
    const channel = await mock.getChannel(decodeURIComponent(channelMatch[1]))
    if (!channel) return jsonResponse({ error: 'not found' }, 404)
    return jsonResponse(channel)
  }
  if (path === '/api/analytics/trends' && method === 'GET') {
    return jsonResponse(
      await mock.getTrends(search.get('from') ?? undefined, search.get('to') ?? undefined, search.get('granularity') ?? 'day')
    )
  }
  if (path === '/api/analytics/route-match-rates' && method === 'GET') {
    return jsonResponse(
      await mock.getRouteMatchRates(search.get('from') ?? undefined, search.get('to') ?? undefined, search.get('granularity') ?? 'day')
    )
  }
  if (path === '/api/analytics/quota' && method === 'GET') {
    return jsonResponse(await mock.getQuota())
  }

  // Overview
  if (path === '/api/overview' && method === 'GET') {
    return jsonResponse(await mock.getOverview())
  }

  // Runs
  if (path === '/api/runs' && method === 'GET') {
    const runs = await mock.listRuns()
    return jsonResponse({ runs })
  }
  const runMatch = path.match(/^\/api\/runs\/(\d+)$/)
  if (runMatch && method === 'GET') {
    const run = await mock.getRun(Number(runMatch[1]))
    if (!run) return jsonResponse({ error: 'not found' }, 404)
    return jsonResponse(run)
  }
  if (path === '/api/runs' && method === 'POST') {
    return jsonResponse(await mock.startRun())
  }
  if (path === '/api/runs/dry-run' && method === 'POST') {
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    return jsonResponse(await mock.startDryRun(body?.draft_configuration))
  }
  if (path === '/api/runs/replay' && method === 'POST') {
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    return jsonResponse(await mock.startReplay(body?.config_version_id))
  }
  if (path === '/api/backtest' && method === 'POST') {
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    return jsonResponse(await mock.startBacktest(body?.proposed_configuration))
  }

  // Routes
  if (path === '/api/routes' && method === 'GET') {
    return jsonResponse({ routes: await mock.listRoutes() })
  }
  if (path === '/api/routes' && method === 'POST') {
    const created = await mock.createRoute(routeFromBody(String(init?.body)))
    return jsonResponse(created, 201)
  }
  const routeMatch = path.match(/^\/api\/routes\/(\d+)$/)
  if (routeMatch) {
    const id = Number(routeMatch[1])
    if (method === 'GET') {
      const route = await mock.getRoute(id)
      if (!route) return jsonResponse({ error: 'not found' }, 404)
      return jsonResponse(route)
    }
    if (method === 'PUT') {
      const updated = await mock.updateRoute(id, routeFromBody(String(init?.body)))
      return jsonResponse(updated)
    }
    if (method === 'DELETE') {
      await mock.deleteRoute(id)
      return new Response(null, { status: 204 })
    }
  }
  if (path === '/api/routes/reorder' && method === 'PUT') {
    const body = JSON.parse(String(init?.body)) as { route_ids: number[] }
    await mock.reorderRoutes(body.route_ids)
    return jsonResponse({ ok: true })
  }

  // Queue
  if (path === '/api/errors' && method === 'GET') {
    return jsonResponse({ errors: await mock.listErrors(search.get('include') ?? 'active') })
  }
  const errorMatch = path.match(/^\/api\/errors\/(\d+)\/(retry|dismiss)$/)
  if (errorMatch && method === 'POST') {
    const id = Number(errorMatch[1])
    const action = errorMatch[2]
    const item = action === 'retry' ? await mock.retryError(id) : await mock.dismissError(id)
    if (!item) return jsonResponse({ error: 'not found' }, 404)
    return jsonResponse(item)
  }
  if (path === '/api/deferred' && method === 'GET') {
    return jsonResponse({ deferred: await mock.listDeferred(search.get('include') ?? 'pending') })
  }
  const deferredMatch = path.match(/^\/api\/deferred\/(\d+)\/(retry|discard)$/)
  if (deferredMatch && method === 'POST') {
    const id = Number(deferredMatch[1])
    const action = deferredMatch[2]
    const item = action === 'retry' ? await mock.retryDeferred(id) : await mock.discardDeferred(id)
    if (!item) return jsonResponse({ error: 'not found' }, 404)
    return jsonResponse(item)
  }

  // Settings / retention playlists
  if (path === '/api/settings' && method === 'GET') {
    return jsonResponse(await mock.getSettings())
  }
  if (path === '/api/settings' && method === 'PUT') {
    const settings = await mock.updateSettings(JSON.parse(String(init?.body)) as Partial<Settings>)
    return jsonResponse(settings)
  }
  if (path === '/api/settings/duplicate-threshold' && method === 'GET') {
    return jsonResponse(await mock.getDuplicateThreshold())
  }
  if (path === '/api/settings/duplicate-threshold' && method === 'PUT') {
    const body = JSON.parse(String(init?.body)) as { threshold: number }
    await mock.setDuplicateThreshold(body.threshold)
    return jsonResponse({ ok: true })
  }
  if (path === '/api/settings/retention-playlists' && method === 'GET') {
    return jsonResponse({ playlists: await mock.listRetentionPlaylists() })
  }
  if (path === '/api/settings/retention-playlists' && method === 'POST') {
    const playlist = await mock.createRetentionPlaylist(JSON.parse(String(init?.body)) as { playlist_id: string; playlist_name: string })
    return jsonResponse(playlist, 201)
  }
  const playlistMatch = path.match(/^\/api\/settings\/retention-playlists\/(\d+)$/)
  if (playlistMatch && method === 'DELETE') {
    await mock.deleteRetentionPlaylist(Number(playlistMatch[1]))
    return new Response(null, { status: 204 })
  }

  // Account
  if (path === '/api/account/status' && method === 'GET') {
    return jsonResponse(await mock.getAccountStatus())
  }
  if (path === '/api/account/' && method === 'DELETE') {
    await mock.disconnectAccount()
    return jsonResponse({ ok: true })
  }

  // Auth
  if (path === '/api/auth/initiate' && method === 'POST') {
    return jsonResponse({ auth_url: 'https://accounts.google.com/oauth/mock', state: 'mock-state' })
  }

  // Config versions
  if (path === '/api/config/versions' && method === 'GET') {
    return jsonResponse({ versions: await mock.listConfigVersions() })
  }
  const versionMatch = path.match(/^\/api\/config\/versions\/(\d+)$/)
  if (versionMatch && method === 'GET') {
    const version = await mock.getConfigVersion(Number(versionMatch[1]))
    if (!version) return jsonResponse({ error: 'not found' }, 404)
    return jsonResponse(version)
  }
  const versionDiffMatch = path.match(/^\/api\/config\/versions\/(\d+)\/diff$/)
  if (versionDiffMatch && method === 'GET') {
    const id = Number(versionDiffMatch[1])
    const compareTo = search.get('compare_to')
    return jsonResponse(await mock.diffConfigVersion(id, compareTo ? Number(compareTo) : undefined))
  }
  const versionRestoreMatch = path.match(/^\/api\/config\/versions\/(\d+)\/restore$/)
  if (versionRestoreMatch && method === 'POST') {
    const id = Number(versionRestoreMatch[1])
    const body = init?.body ? JSON.parse(String(init.body)) : {}
    return jsonResponse(await mock.restoreConfigVersion(id, body.description), 201)
  }

  return jsonResponse({ error: 'not implemented in test mock' }, 501)
}
