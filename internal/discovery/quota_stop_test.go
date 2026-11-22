package discovery

import (
	"context"
	"database/sql"
	"testing"

	"github.com/bateau84/YuHaul/internal/quota"
	"github.com/stretchr/testify/require"
)

// Tests for ARS-001 section 4: a quota refusal before the channel scan ends
// discovery as cut_short, not as an error.

func (h *isoHarness) ledgerRows(t *testing.T, category string) int {
	t.Helper()
	var n int
	require.NoError(t, h.db.QueryRow(`SELECT COUNT(*) FROM quota_ledger WHERE outcome_category = ?`, category).Scan(&n))
	return n
}

func (h *isoHarness) interruptReason(t *testing.T) sql.NullString {
	t.Helper()
	var r sql.NullString
	require.NoError(t, h.db.QueryRow(`SELECT interrupt_reason FROM run WHERE id = ?`, h.runID).Scan(&r))
	return r
}

func requirePreScanQuotaStop(t *testing.T, h *isoHarness, res *Result, err error) {
	t.Helper()
	require.NoError(t, err)
	require.True(t, res.ScanStopped)
	require.True(t, res.Deferred)
	require.NotEmpty(t, res.DeferredReason)
	require.Zero(t, res.VideosDiscovered)
	outcomeIs(t, h.scanOutcome(t), "cut_short")
	require.False(t, h.interruptReason(t).Valid, "a quota stop is not an interruption")
	require.Zero(t, h.requests('A')+h.requests('B'), "no uploads request after the stop")
	require.Contains(t, h.logs.String(), "discovery skipped: daily quota used up")
}

// Q7: the local budget is used up before discovery.
func TestPreScanLocalBudgetExhaustedEndsCutShort(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"})}, 1)
	require.NoError(t, h.ledger.Record(context.Background(), sql.NullInt64{}, "videos_list"))
	res, err := h.run()
	requirePreScanQuotaStop(t, h, res, err)
	require.Contains(t, h.logs.String(), "refusal=admission")
	require.Zero(t, h.ledgerRows(t, "success")+h.ledgerRows(t, "quota"), "nothing reached the provider")
}

// Q8: the first subscriptions.list is refused by the provider.
func TestPreScanProviderQuotaOnSubscriptionsEndsCutShort(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"})}, 1000)
	h.subsFail = true
	res, err := h.run()
	requirePreScanQuotaStop(t, h, res, err)
	require.Contains(t, h.logs.String(), "refusal=provider")
	require.Contains(t, h.logs.String(), "operation=subscriptions_list")
	require.Equal(t, 1, h.ledgerRows(t, "quota"))
}

// Q9: the second channels.list batch is refused. No uploads request follows.
func TestPreScanQuotaOnSecondChannelsBatchEndsCutShort(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"}), okSpec('B', []string{"vB1"})}, 1000)
	h.engine.batchSize = 1
	h.channelsFailCall = 2
	res, err := h.run()
	requirePreScanQuotaStop(t, h, res, err)
	require.Contains(t, h.logs.String(), "operation=channels_list")
	require.Equal(t, 1, h.ledgerRows(t, "quota"))
	require.Equal(t, 2, h.channelCalls)
}

// Q10: a storage fault in the in-loop budget check is an error, not a quota stop.
func TestInLoopBudgetStorageFaultFailsDiscovery(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{okSpec('A', []string{"vA1"})}, 1000)
	h.breakLedgerAfterChannels = true
	res, err := h.run()
	require.Error(t, err)
	require.Nil(t, res)
	require.NotErrorIs(t, err, ErrNoSourceScanned)
	require.NotContains(t, h.logs.String(), "discovery skipped")
	require.Zero(t, h.requests('A'))
}

// An admitted provider quota refusal inside the channel loop stops the scan
// (ARS-001 section 4.3) instead of failing the run.
func TestProviderQuotaInChannelLoopStopsScan(t *testing.T) {
	h := newIsoHarness(t, []srcSpec{
		okSpec('A', []string{"vA1"}),
		{failStatus: 403, failBody: quotaExceededBody, id: chID('B'), pages: [][]string{{"vB1"}}, failAt: 1},
		okSpec('C', []string{"vC1"}),
	}, 1000)
	// The pipeline scopes the context to the run; the run's quota stop then
	// refuses channel C before any request.
	res, err := h.engine.RunBound(quota.WithRun(context.Background(), h.runID), h.runID, h.binding)
	require.NoError(t, err)
	require.True(t, res.ScanStopped)
	outcomeIs(t, h.scanOutcome(t), "cut_short")
	require.Equal(t, 0, h.requests('C'), "scan stops at the refusal")
	require.Equal(t, 1, h.videoCount(t, "vA1"), "videos found before the stop are kept")
	require.Equal(t, 1, h.ledgerRows(t, "quota"))
}
