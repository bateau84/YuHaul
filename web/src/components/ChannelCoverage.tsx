import * as sourceApi from '../api/source-failures'
import { useSourceFailures } from '../hooks/useSourceFailures'
import { showSourceChannelId, sourceChannelLabel, sourceReasonSentence, UPLOADS_NOT_SCANNED } from '../lib/source-failure'
import type { Run } from '../types'

// Run Detail "Channels not scanned" block (discovery ARS-002 section 4.4 and the
// design's "Channel scan history and coverage block"). It reads its own
// request, so a slow or failed read never blocks the page, and it says nothing
// at all for a run with no recorded failure: the record cannot tell a run with
// no failed channel from an older run with no scan evidence.
//
// The wording is chosen from the run's saved scan outcome (run.discovery_scan)
// and status only, by the first matching rule of ARS-002 section 4.5:
//  - failed and no_source_scanned: the run ended because no channel was scanned;
//  - cut_short: scanning stopped before it finished because of a run-wide cause;
//  - completed (or no_source_scanned on another status): the scan finished normally;
//  - in_progress: the run is still scanning (copy pending the designer);
//  - unknown (null or absent, unreachable when rows exist): "did not complete
//    normally" for a failed, interrupted or stopped run, and only the list
//    otherwise.

type Variant = 'none_scanned' | 'cut_short' | 'partial' | 'not_established' | 'list_only'

function variantFor(run: Run): Variant {
  const scan = run.discovery_scan ?? null
  if (run.status === 'failed' && scan === 'no_source_scanned') return 'none_scanned'
  if (scan === 'cut_short') return 'cut_short'
  if (scan === 'completed' || scan === 'no_source_scanned' || scan === 'in_progress') return 'partial'
  return run.status === 'failed' || run.status === 'interrupted' || run.status === 'stopped' ? 'not_established' : 'list_only'
}

const HEADING: Record<Exclude<Variant, 'list_only'>, string> = {
  none_scanned: 'Run failed. No channels were scanned successfully.',
  cut_short: 'Channel scanning stopped before it finished. Results are incomplete.',
  partial: 'Some channels could not be scanned. Results are incomplete.',
  not_established: 'This run did not complete normally, so results are incomplete. See the run status.',
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
      <section
        className="card"
        {...(variant === 'list_only' || failures.length === 0
          ? { 'aria-label': 'Channel scan details' }
          : { 'aria-labelledby': 'channel-coverage-heading' })}
        style={{ marginBottom: 16 }}
      >
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
            {variant !== 'list_only' && (
              <h2 id="channel-coverage-heading" className="section-title">{HEADING[variant]}</h2>
            )}
            {variant === 'cut_short' && <p style={{ margin: '0 0 8px' }}>See the run status for the cause.</p>}
            <p style={{ margin: '0 0 8px' }}>
              {variant === 'cut_short' || variant === 'not_established' || variant === 'list_only'
                ? 'Channels recorded as not scanned:'
                : 'Channels not scanned:'}
            </p>
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
            {(variant === 'cut_short' || variant === 'not_established') && (
              <p style={{ margin: '8px 0 0' }}>YuHaul has no record of which other channels were scanned.</p>
            )}
            {variant === 'cut_short' && run.status === 'completed' && (
              <p style={{ margin: '8px 0 0' }}>
                Completed is the run&apos;s saved status; it does not mean every channel was scanned.
              </p>
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
