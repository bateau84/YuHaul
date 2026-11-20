import type { RequeueResult, StrandedPreview } from '../types'
import { api } from './core'

// Preview of the stranded videos a re-queue would add (ARS-005 §8.1).
export async function getStrandedPreview(): Promise<StrandedPreview> {
  return api<StrandedPreview>('/api/stranded-videos')
}

// Confirm the re-queue. expectedRequeue is the preview's requeue count; the
// server reports differs_from_preview when the result differs (ARS-005 §8.2).
// Refusals reject with ApiError carrying status and code (RequeueRefusalCode).
export async function requeueStranded(expectedRequeue?: number): Promise<RequeueResult> {
  const body = expectedRequeue === undefined ? undefined : JSON.stringify({ expected_requeue: expectedRequeue })
  return api<RequeueResult>('/api/stranded-videos/requeue', { method: 'POST', body })
}
