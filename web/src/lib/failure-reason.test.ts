import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import {
  categoryLabel,
  DIAG_CODES,
  DIAG_OAUTH_REASONS,
  DIAG_OPERATIONS,
  DIAG_PHASES,
  DIAG_PROVIDER_REASONS,
  DIAG_SQLITE,
  failureSentence,
  offersAccountStatus,
  parseDiag,
  reasonRunLink,
  REASON_UNAVAILABLE,
} from './failure-reason'

// A shape internal/diag admits for each code (some codes bind their own phase and operation).
const SHAPE: Record<string, string> = {
  video_unavailable: 'phase=routing operation=load_metadata',
  no_source_scanned: 'phase=discovery operation=discover',
  stopped_by_user: 'phase=unknown operation=stop_run',
  ended_by_user: 'phase=unknown operation=end_legacy_run',
  snapshot_unavailable: 'phase=unknown operation=discard_run',
}
const diag = (code: string, extra = '') => `diag:v1 ${SHAPE[code] ?? 'phase=action_execution operation=execute_action'} code=${code}${extra}`
const provider = (reason: string) => diag('provider_error', ` status=403 reason=${reason}`)

// Every row of the design table ("Errors — Category and Reason Presentation").
describe('failureSentence', () => {
  it.each([
    ['quotaExceeded', "YouTube's API quota was used up."],
    ['dailyLimitExceeded', "YouTube's API quota was used up."],
    ['rateLimitExceeded', 'YouTube asked YuHaul to slow down.'],
    ['userRateLimitExceeded', 'YouTube asked YuHaul to slow down.'],
    ['authError', "YouTube did not accept YuHaul's access for this request."],
    ['forbidden', "YouTube did not accept YuHaul's access for this request."],
    ['insufficientPermissions', "YouTube did not accept YuHaul's access for this request."],
    ['backendError', 'YouTube reported a problem on its side.'],
    ['internalError', 'YouTube reported a problem on its side.'],
    ['badRequest', 'YouTube rejected the request as invalid.'],
    ['invalidParameter', 'YouTube rejected the request as invalid.'],
    ['videoNotFound', 'YouTube could not find this video.'],
    ['playlistNotFound', 'YouTube could not find the playlist.'],
    ['playlistItemsNotAccessible', "YouTube would not let YuHaul read the playlist's items."],
    ['notFound', 'YouTube could not find what was requested.'],
    ['playlistContainsMaximumNumberOfVideos', 'YouTube reported that the playlist is full.'],
    ['videoAlreadyInPlaylist', 'YouTube reported that the video is already in the playlist.'],
  ])('provider_error reason %s', (reason, sentence) => {
    expect(failureSentence(provider(reason))).toBe(sentence)
  })

  it('provider_error without a reason uses the status only when one is stored; an unlisted reason is not trusted', () => {
    expect(failureSentence(diag('provider_error'))).toBe('YouTube returned an error.')
    expect(failureSentence(diag('provider_error', ' status=503'))).toBe('YouTube returned an error (status 503).')
    expect(failureSentence(diag('provider_error', ' status=503 reason=somethingNew'))).toBe('Reason unavailable.')
  })

  it('oauth_error: invalid_grant, any other reason or none', () => {
    expect(failureSentence(diag('oauth_error', ' reason=invalid_grant'))).toBe("YouTube no longer accepts YuHaul's saved access.")
    expect(failureSentence(diag('oauth_error', ' reason=invalid_client'))).toBe('YouTube access could not be renewed.')
    expect(failureSentence(diag('oauth_error'))).toBe('YouTube access could not be renewed.')
  })

  it.each([
    ['authorization_unavailable', 'No usable YouTube access was available.'],
    ['authorization_changed', 'YouTube access changed while this was in progress.'],
    ['credential_persistence_failed', 'YuHaul could not save renewed YouTube access.'],
    ['action_authority_invalid', "YuHaul could not confirm that this run's saved routes allow this playlist change."],
    ['mutation_application_uncertain', 'YuHaul could not confirm whether this playlist action was applied. Repeating it could add the video again, so it remains blocked.'],
    ['no_source_scanned', 'No channels were scanned successfully.'],
    ['video_unavailable', 'YouTube did not return this video when YuHaul asked for its details.'],
    ['timeout', 'YuHaul stopped waiting because a step took too long.'],
    ['transport_error', 'A network connection failed.'],
    ['invalid_provider_response', "YouTube's answer could not be understood."],
    ['sqlite_error', 'YuHaul could not read or save its own data.'],
    ['canceled', 'The work was cancelled before YuHaul got a result.'],
    ['stopped_by_user', 'Stopped by you before it finished.'],
    ['ended_by_user', 'Marked as ended by you.'],
    ['panic', 'YuHaul hit an unexpected internal fault.'],
    ['unknown_error', 'The cause of this failure is not available.'],
  ])('code %s', (code, sentence) => {
    expect(failureSentence(diag(code))).toBe(sentence)
  })

  it('reads "Reason unavailable." for run-level, unlisted and prototype-named codes', () => {
    for (const code of ['snapshot_unavailable', 'snapshot_unreadable', 'checkpoint_unrecognized', 'brand_new_code', 'constructor', 'toString', 'original_decision_unavailable']) {
      expect(failureSentence(diag(code))).toBe(REASON_UNAVAILABLE)
    }
  })

  // The designed sentences for the codes added 2026-11-20 (design, "Added
  // codes"). The two retry-run codes take the run they describe.
  it('reads stop_settlement_blocked as designed', () => {
    expect(failureSentence(diag('stop_settlement_blocked'))).toBe(
      "YuHaul could not safely finish stopping this run, so the stop is not settled. This run will not be resumed. Nothing on this page settles it; check YuHaul's logs.",
    )
  })

  describe('retry-run codes by context', () => {
    const claim = (subject: string) => `YuHaul could not verify which queue items ${subject} had claimed, so it stopped instead of continuing that work.`
    it.each([
      ['own run', { kind: 'own' } as const, `${claim('this run')} The videos below show what was recorded.`],
      ['a run id', { kind: 'id', id: 41 } as const, `${claim('run 41')} Open the run to see what was recorded.`],
      ['no run id', { kind: 'unknown' } as const, claim('a retry run')],
    ])('retry_claim_unverifiable, %s', (_name, run, sentence) => {
      expect(failureSentence(diag('retry_claim_unverifiable'), run)).toBe(sentence)
    })
    it.each([
      ['own run', { kind: 'own' } as const, 'This retry stopped because the item it was working on was removed by you. The videos below show what was recorded.'],
      ['a run id', { kind: 'id', id: 41 } as const, 'Run 41, a retry, stopped because the item it was working on was removed by you. Open the run to see what was recorded.'],
      ['no run id', { kind: 'unknown' } as const, 'A retry stopped because the item it was working on was removed by you.'],
    ])('retry_work_removed, %s', (_name, run, sentence) => {
      expect(failureSentence(diag('retry_work_removed'), run)).toBe(sentence)
    })
    it('uses the no-id wording by default and for an unusable id, and never claims anything was or was not sent', () => {
      for (const code of ['retry_claim_unverifiable', 'retry_work_removed']) {
        const none = failureSentence(diag(code))
        expect(failureSentence(diag(code), { kind: 'id', id: 0 })).toBe(none)
        expect(failureSentence(diag(code), { kind: 'id', id: 1.5 })).toBe(none)
        for (const run of [{ kind: 'own' }, { kind: 'id', id: 3 }, { kind: 'unknown' }] as const) {
          expect(failureSentence(diag(code), run)).not.toMatch(/nothing was sent|was not sent|was sent/i)
        }
      }
    })
    it('links only a retry-run code with a run id the API supplied', () => {
      expect(reasonRunLink(diag('retry_claim_unverifiable'), { kind: 'id', id: 41 })).toBe(41)
      expect(reasonRunLink(diag('retry_work_removed'), { kind: 'id', id: 41 })).toBe(41)
      expect(reasonRunLink(diag('retry_work_removed'), { kind: 'own' })).toBeNull()
      expect(reasonRunLink(diag('retry_work_removed'), { kind: 'unknown' })).toBeNull()
      expect(reasonRunLink(diag('stop_settlement_blocked'), { kind: 'id', id: 41 })).toBeNull()
      expect(reasonRunLink(diag('timeout'), { kind: 'id', id: 41 })).toBeNull()
      expect(reasonRunLink('not a diag string', { kind: 'id', id: 41 })).toBeNull()
    })
  })

  it('registers the three codes and admits no other facts with them', () => {
    for (const code of ['retry_claim_unverifiable', 'stop_settlement_blocked', 'retry_work_removed']) {
      expect(DIAG_CODES.has(code)).toBe(true)
      const text = diag(code)
      expect(parseDiag(text)).toEqual({ code, status: undefined, reason: undefined })
      expect(parseDiag(`${text} status=500`)).toBeNull()
      expect(parseDiag(`${text} reason=backendError`)).toBeNull()
      expect(offersAccountStatus(text)).toBe(false)
    }
  })

  it('never returns raw text: a string outside the diag:v1 grammar reads "Reason unavailable."', () => {
    for (const text of ['quotaExceeded', 'HTTP 500 boom', 'diag:v1 phase=a code=timeout', 'diag:v1 phase=a operation=b code=timeout extra=1', 'diag:v1 phase=a operation=b code=timeout junk', '']) {
      expect(failureSentence(text)).toBe(REASON_UNAVAILABLE)
      expect(parseDiag(text)).toBeNull()
    }
  })
})

describe('categoryLabel', () => {
  it('labels the three categories and reads "Error" for anything else', () => {
    expect(categoryLabel('api_error')).toBe('YouTube API error')
    expect(categoryLabel('authorization')).toBe('Authorization')
    expect(categoryLabel('runtime')).toBe('Processing error')
    expect(categoryLabel('something_else')).toBe('Error')
    expect(categoryLabel(undefined)).toBe('Error')
  })
})

// The browser's closed value sets must equal internal/diag's (ARS-001).
describe('closed diag:v1 value sets', () => {
  const go = readFileSync(join(process.cwd(), '..', 'internal/diag/diag.go'), 'utf8')
  const goSet = (name: string) => new Set(new RegExp(`var ${name} = set\\("([^"]+)"\\)`).exec(go)![1].split(' '))
  it.each([
    ['phases', DIAG_PHASES],
    ['operations', DIAG_OPERATIONS],
    ['codes', DIAG_CODES],
    ['providerReasons', DIAG_PROVIDER_REASONS],
    ['oauthReasons', DIAG_OAUTH_REASONS],
  ])('%s match diag.go', (name, ts) => {
    expect([...ts].sort()).toEqual([...goSet(name)].sort())
  })
  it('sqlite codes match diag.go', () => {
    const names = [...go.matchAll(/\d+: "(SQLITE_[A-Z]+)"/g)].map((m) => m[1])
    expect([...DIAG_SQLITE].sort()).toEqual(names.sort())
  })

  const base = 'diag:v1 phase=action_execution operation=execute_action'
  it('rejects values outside the sets: they read "Reason unavailable." and get no disclosure', () => {
    for (const text of [
      'diag:v1 phase=bogus operation=execute_action code=timeout',
      `diag:v1 phase=action_execution operation=bogus code=timeout`,
      `${base} code=bogus_code`,
      `${base} code=provider_error status=403 reason=madeUp`,
      `${base} code=provider_error status=403 reason=constructor`,
      `${base} code=provider_error status=200`,
      `${base} code=provider_error status=0403`,
      `${base} code=timeout reason=quotaExceeded`,
      `${base} code=oauth_error reason=quotaExceeded`,
      `${base} code=provider_error reason=invalid_grant`,
      `${base} code=timeout status=500`,
      `${base} code=sqlite_error sqlite=SQLITE_MADE_UP`,
      `${base} code=authorization_changed status=500`,
      'diag:v1 phase=unknown operation=discard_run code=timeout',
      'diag:v1 phase=action_execution operation=execute_action code=original_decision_unavailable',
      `${base} code=no_source_scanned`,
      'diag:v1 phase=discovery operation=discover code=no_source_scanned status=404',
    ]) {
      expect(parseDiag(text), text).toBeNull()
      expect(failureSentence(text), text).toBe(REASON_UNAVAILABLE)
    }
  })

  it('admits what diag.go admits', () => {
    for (const text of [
      `${base} code=provider_error status=403 reason=quotaExceeded`,
      `${base} code=oauth_error status=400 reason=invalid_grant`,
      `${base} code=sqlite_error sqlite=SQLITE_BUSY`,
      'diag:v1 phase=unknown operation=discard_run code=snapshot_unavailable',
      'diag:v1 phase=routing operation=load_metadata code=video_unavailable',
      'diag:v1 phase=discovery operation=discover code=no_source_scanned',
    ]) expect(parseDiag(text), text).not.toBeNull()
  })
})

describe('offersAccountStatus', () => {
  const d = (code: string, extra = '') => `diag:v1 phase=action_execution operation=execute_action code=${code}${extra}`
  it('is true for the account codes only', () => {
    for (const c of ['oauth_error', 'authorization_unavailable', 'authorization_changed', 'credential_persistence_failed']) expect(offersAccountStatus(d(c))).toBe(true)
    for (const c of ['provider_error', 'timeout', 'transport_error', 'unknown_error']) expect(offersAccountStatus(d(c))).toBe(false)
    expect(offersAccountStatus('oauth_error')).toBe(false)
    expect(offersAccountStatus(undefined)).toBe(false)
  })
})
