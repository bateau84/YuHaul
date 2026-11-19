import * as sourceApi from '../api/source-failures'
import { useSourceFailures } from '../hooks/useSourceFailures'
import { parseDiag } from '../lib/failure-reason'
import { showSourceChannelId, sourceChannelLabel, sourceReasonSentence, UPLOADS_NOT_SCANNED } from '../lib/source-failure'
import type { Run } from '../types'

// Run Detail "Channels not scanned" block (discovery ARS-002 section 4.4 and the
// design's "Channel scan history and coverage block"). It reads its own
// request, so a slow or failed read never blocks the page, and it says nothing
// at all for a run with no recorded failure: the record cannot tell a run with
// no failed channel from an older run with no scan evidence.
//
// The wording is chosen from established facts only:
//  - the run ended because no channel was scanned (diag code no_source_scanned);
//  - the run failed, was interrupted or stopped for another run-wide reason, so
//    the scan stage is not established;
//  - otherwise the run's scan is taken as finished normally.
// The API has no per-run fact that scanning was cut short by a run-wide cause,
// so that variant ("Channel scanning stopped before it finished") cannot be
// chosen; for a failed or interrupted run the not-established variant applies.

type Variant = 'none_scanned' | 'not_established' | 'partial'

function variantFor(run: Run): Variant {
  if (run.status === 'failed' && parseDiag(run.interrupt_reason)?.code === 'no_source_scanned') return 'none_scanned'
  if (run.status === 'failed' || run.status === 'interrupted' || run.status === 'stopped') return 'not_established'
  return 'partial'
}

export function ChannelCoverage({ run }: { run: Run }) {
  const list = useSourceFailures((before) => sourceApi.getRunSourceFailures(run.id, { beforeId: before }), `${run.id}:${run.status}`)
  // Only permanent failures are designed here; other kinds are not shown.
  const failures = list.items.filter((f) => f.category === 'permanent')

  if (list.load === 'loading' && list.items.length === 0) {
    return <p role="status" style={{ margin: '0 0 16px' }}>Loading channel scan details…</p>
  }
  if (list.load === 'failed' || failures.length > 0) {
    const variant = variantFor(run)
    return (
      <section className="card" aria-labelledby="channel-coverage-heading" style={{ marginBottom: 16 }}>
        {list.load === 'failed' && (
          <div role="alert" style={{ color: 'var(--color-status-failed)', marginBottom: 8 }}>
            <p style={{ margin: 0 }}>Could not load channel scan details. Nothing was changed.</p>
            {list.stale && <p style={{ margin: '4px 0 0' }}>The channels below may be out of date.</p>}
            <button type="button" className="button" onClick={list.reload} style={{ marginTop: 8 }}>
              Reload channel scan details
            </button>
          </div>
        )}
        {failures.length > 0 && (
          <>
            <h2 id="channel-coverage-heading" className="section-title">
              {variant === 'none_scanned'
                ? 'Run failed. No channels were scanned successfully.'
                : variant === 'not_established'
                  ? 'This run did not complete normally, so results are incomplete. See the run status.'
                  : 'Some channels could not be scanned. Results are incomplete.'}
            </h2>
            <p style={{ margin: '0 0 8px' }}>{variant === 'not_established' ? 'Channels recorded as not scanned:' : 'Channels not scanned:'}</p>
            <ul style={{ listStyle: 'none', padding: 0, margin: 0, display: 'grid', gap: 12 }}>
              {failures.map((f) => (
                <li key={f.id} style={{ overflowWrap: 'anywhere' }}>
                  <div>{sourceChannelLabel(f)}</div>
                  {showSourceChannelId(f) && <div>Channel ID: {f.channel_id}</div>}
                  <div>
                    {UPLOADS_NOT_SCANNED} {sourceReasonSentence(f)}
                  </div>
                </li>
              ))}
            </ul>
            {variant === 'not_established' && (
              <p style={{ margin: '8px 0 0' }}>YuHaul has no record of which other channels were scanned.</p>
            )}
            {list.hasMore && (
              <button type="button" className="button" onClick={list.loadMore} disabled={list.loadingMore} style={{ marginTop: 12 }}>
                Show more channels
              </button>
            )}
          </>
        )}
      </section>
    )
  }
  return null
}
