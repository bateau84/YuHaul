// API client boundary. Currently backed by in-memory mocks so the SPA can be
// built and verified before all backend endpoints are wired. Set
// VITE_USE_MOCK=false to switch to the real REST endpoints.
export * from './mock'
export { USE_MOCK } from './mock'
