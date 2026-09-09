// API client boundary. Operations with wired backend endpoints call the real
// REST API; remaining operations fall back to the in-memory mock layer. Set
// VITE_USE_MOCK=false to disable mock-backed functions where a real endpoint
// exists.
export { USE_MOCK } from './mock'

// Route CRUD is wired to the real backend.
export {
  listRoutes,
  getRoute,
  createRoute,
  updateRoute,
  deleteRoute,
  reorderRoutes,
} from './routes'

// Run list/detail/start operations are wired to the real backend.
export {
  listRuns,
  getRun,
  startRun,
  startDryRun,
  startReplay,
  startBacktest,
} from './runs'

// Overview is wired to the real backend.
export { getOverview } from './overview'

// Queue operations are wired to the real backend.
export {
  listErrors,
  retryError,
  dismissError,
  listDeferred,
  retryDeferred,
  discardDeferred,
} from './queue'

// Analytics operations are wired to the real backend.
export {
  listChannels,
  getChannel,
  getTrends,
  getRouteMatchRates,
  getQuota,
  listRetentionPlaylists,
  createRetentionPlaylist,
  deleteRetentionPlaylist,
} from './analytics'

// Account operations are wired to the real backend.
export { getAccountStatus, disconnectAccount } from './account'

// Duplicate-threshold settings are wired to the real backend.
export { getDuplicateThreshold, setDuplicateThreshold } from './settings'

// The following operations do not yet have backend endpoints and remain
// mock-backed.
export {
  getRunVideos,
  getDecisionDetail,
  listConfigVersions,
  getConfigVersion,
  getSettings,
  updateSettings,
  listAdvisories,
  dismissAdvisory,
  ignoreAdvisory,
  actOnAdvisory,
  subscribeRunSSE,
} from './mock'
