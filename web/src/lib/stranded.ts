import type { StrandedReport } from '../types'

// Number and report helpers shared by the stranded-video preview page and its
// report component (design: "Re-queue Stranded Videos and the Paged Deferred Tab").

const nf = new Intl.NumberFormat()
export const fmt = (n: number) => nf.format(n)
export const videosText = (n: number) => `${fmt(n)} ${n === 1 ? 'video' : 'videos'}`

// A Go server can marshal an empty list or map as null. The views read the
// collections as always present, so a response is normalized once on arrival.
export function normalizeReport<T extends StrandedReport>(r: T): T {
  return {
    ...r,
    blocked: { ...r.blocked, by_reason: r.blocked?.by_reason ?? {}, items: r.blocked?.items ?? [] },
    not_ended: { ...r.not_ended, runs: r.not_ended?.runs ?? [] },
  }
}
