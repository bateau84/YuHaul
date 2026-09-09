// API client boundary. Route operations call the real REST endpoints;
// remaining operations are still backed by in-memory mocks until their
// backend endpoints are wired. Set VITE_USE_MOCK=false to switch the mock
// layer to real implementations once they exist.
export * from './mock'
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
