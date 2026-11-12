// Package diag owns the closed diagnostic vocabulary released by persistence,
// APIs, events and logs. Error prose is never a diagnostic fact.
package diag

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"

	"github.com/bateau84/YuHaul/internal/youtube"
	"golang.org/x/oauth2"
	"modernc.org/sqlite"
)

const MaxReasonLen = 200

type panicCause struct{}

func (panicCause) Error() string { return "panic" }

// PanicCause marks a recovered panic without retaining or formatting its value.
func PanicCause() error { return panicCause{} }

type operationCause struct {
	cause     error
	phase     string
	operation string
}

func (e *operationCause) Error() string { return "operation failed" }
func (e *operationCause) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// At attaches source-owned phase and operation facts to an error without
// deriving them from its text. The wrapper's Error string is intentionally
// fixed; callers should still classify before any public or log boundary.
func At(err error, phase, operation string) error {
	if err == nil {
		return nil
	}
	return &operationCause{cause: err, phase: phase, operation: operation}
}

type codeCause struct {
	cause error
	code  string
}

func (e *codeCause) Error() string { return "operation failed" }
func (e *codeCause) Unwrap() error { return e.cause }

// WithCode marks err with a registered code that carries no provider facts.
// Only mutation_application_uncertain is accepted: it names a guard-blocked or
// admitted-but-unconfirmed mutation. Other codes are ignored.
func WithCode(err error, code string) error {
	if err == nil || code != "mutation_application_uncertain" {
		return err
	}
	return &codeCause{cause: err, code: code}
}

type Facts struct{ Phase, Operation, Code, Status, Reason, SQLite string }

var phases = set("unknown discovery exclusion duplicate_detection routing action_execution")
var operations = set("unknown start_run load_run load_token select_inputs capture_selection discover load_metadata apply_exclusions detect_duplicates evaluate_video record_decision execute_action record_action retain_failure retry finalize_run record_phase inspect_run inspect_queue account_quota generate_advisory parse_response subscriptions_list channels_list playlist_items_list videos_list playlist_items_insert playlists_list refresh_token save_token account_status disconnect_account oauth_callback dispatch_request discard_run stop_run end_legacy_run requeue_stranded video_search")
var providerReasons = set("quotaExceeded dailyLimitExceeded rateLimitExceeded userRateLimitExceeded authError forbidden insufficientPermissions backendError internalError badRequest invalidParameter notFound playlistNotFound videoNotFound playlistItemsNotAccessible playlistContainsMaximumNumberOfVideos videoAlreadyInPlaylist")
var oauthReasons = set("invalid_request invalid_client invalid_grant unauthorized_client unsupported_grant_type invalid_scope access_denied temporarily_unavailable server_error")
var sqliteCodes = map[int]string{1: "SQLITE_ERROR", 5: "SQLITE_BUSY", 6: "SQLITE_LOCKED", 8: "SQLITE_READONLY", 10: "SQLITE_IOERR", 11: "SQLITE_CORRUPT", 13: "SQLITE_FULL", 14: "SQLITE_CANTOPEN", 19: "SQLITE_CONSTRAINT"}
var codes = set("unknown_error sqlite_error provider_error oauth_error timeout canceled transport_error invalid_provider_response panic authorization_unavailable action_authority_invalid credential_persistence_failed authorization_changed mutation_application_uncertain snapshot_unavailable snapshot_unreadable checkpoint_unrecognized stopped_by_user ended_by_user video_unavailable original_decision_unavailable")

// discardCodes are the codes bound to the discard_run operation; stop_run and
// end_legacy_run each bind one code of their own; each operation accepts only its own codes and no other facts.
var discardCodes = set("snapshot_unavailable snapshot_unreadable checkpoint_unrecognized")

func set(s string) map[string]bool {
	m := map[string]bool{}
	for _, v := range strings.Fields(s) {
		m[v] = true
	}
	return m
}

func (f Facts) Valid() bool {
	if !phases[f.Phase] || !operations[f.Operation] || !codes[f.Code] {
		return false
	}
	if f.Code == "original_decision_unavailable" {
		// BR-039 / ARS-006 §1.3: valid only for action_execution/load_run, no other fact.
		return f.Phase == "action_execution" && f.Operation == "load_run" &&
			f.Status == "" && f.Reason == "" && f.SQLite == ""
	}
	if f.Code == "video_unavailable" {
		// Valid only for routing/load_metadata with no other fact. The
		// requeue_stranded operation is a log-only operation (ARS-005 §1.3).
		return f.Phase == "routing" && f.Operation == "load_metadata" &&
			f.Status == "" && f.Reason == "" && f.SQLite == ""
	}
	if f.Operation == "discard_run" || f.Operation == "stop_run" || f.Operation == "end_legacy_run" || discardCodes[f.Code] || f.Code == "stopped_by_user" || f.Code == "ended_by_user" {
		okDiscard := f.Operation == "discard_run" && discardCodes[f.Code]
		okStop := f.Operation == "stop_run" && f.Code == "stopped_by_user"
		okEnd := f.Operation == "end_legacy_run" && f.Code == "ended_by_user"
		return (okDiscard || okStop || okEnd) && f.Status == "" && f.Reason == "" && f.SQLite == ""
	}
	if f.Status != "" {
		n, e := strconv.Atoi(f.Status)
		if e != nil || strconv.Itoa(n) != f.Status || (f.Code == "provider_error" && (n < 300 || n > 599)) || (f.Code == "oauth_error" && (n < 100 || n > 599)) || (f.Code != "provider_error" && f.Code != "oauth_error") {
			return false
		}
	}
	if (f.Code == "credential_persistence_failed" || f.Code == "authorization_changed" || f.Code == "mutation_application_uncertain") && (f.Status != "" || f.Reason != "" || f.SQLite != "") {
		return false
	}
	if f.Reason != "" && !((f.Code == "provider_error" && providerReasons[f.Reason]) || (f.Code == "oauth_error" && oauthReasons[f.Reason])) {
		return false
	}
	if f.SQLite != "" && (f.Code != "sqlite_error" || !strings.Contains(" "+strings.Join(values(sqliteCodes), " ")+" ", " "+f.SQLite+" ")) {
		return false
	}
	return true
}
func values(m map[int]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func (f Facts) String() string {
	if !f.Valid() {
		f = Facts{Phase: "unknown", Operation: "unknown", Code: "unknown_error"}
	}
	s := fmt.Sprintf("diag:v1 phase=%s operation=%s code=%s", f.Phase, f.Operation, f.Code)
	if f.Status != "" {
		s += " status=" + f.Status
	}
	if f.Reason != "" {
		s += " reason=" + f.Reason
	}
	if f.SQLite != "" {
		s += " sqlite=" + f.SQLite
	}
	return s
}

// Classify releases only facts carried by registered concrete error types.
func Classify(err error, phase, operation string) Facts {
	f := Facts{Phase: phase, Operation: operation, Code: "unknown_error"}
	if !phases[phase] {
		f.Phase = "unknown"
	}
	if !operations[operation] {
		f.Operation = "unknown"
	}
	if err == nil {
		return f
	}
	if at, ok := findCause[*operationCause](err); ok {
		if phases[at.phase] {
			f.Phase = at.phase
		}
		if operations[at.operation] {
			f.Operation = at.operation
		}
	}
	if _, ok := findCause[panicCause](err); ok {
		f.Code = "panic"
		return f
	}
	if cc, ok := findCause[*codeCause](err); ok {
		f.Code = cc.code
		return f
	}
	if se, ok := findCause[*sqlite.Error](err); ok {
		f.Code = "sqlite_error"
		if c, ok := sqliteCodes[se.Code()&0xff]; ok {
			f.SQLite = c
		}
		return f
	}
	if ae, ok := findCause[*youtube.APIError](err); ok {
		f.Code = "provider_error"
		if ae.Status >= 300 && ae.Status <= 599 {
			f.Status = strconv.Itoa(ae.Status)
		}
		if providerReasons[ae.Reason] {
			f.Reason = ae.Reason
		}
		return f
	}
	if oe, ok := findCause[*oauth2.RetrieveError](err); ok {
		f.Code = "oauth_error"
		if oe.Response != nil && oe.Response.StatusCode >= 100 && oe.Response.StatusCode <= 599 {
			f.Status = strconv.Itoa(oe.Response.StatusCode)
		}
		if oauthReasons[oe.ErrorCode] {
			f.Reason = oe.ErrorCode
		}
		return f
	}
	if malformed, ok := findCause[*youtube.InvalidResponseError](err); ok {
		f.Code = "invalid_provider_response"
		if operations[string(malformed.Operation())] {
			f.Operation = string(malformed.Operation())
		}
		return f
	}
	if hasCause(err, context.DeadlineExceeded) {
		f.Code = "timeout"
		return f
	}
	if hasCause(err, context.Canceled) {
		f.Code = "canceled"
		return f
	}
	if ne, ok := findCause[net.Error](err); ok && ne.Timeout() {
		f.Code = "timeout"
		return f
	}
	if _, ok := findCause[*url.Error](err); ok {
		f.Code = "transport_error"
		return f
	}
	if _, ok := findCause[*net.OpError](err); ok {
		f.Code = "transport_error"
	}
	return f
}

// findCause traverses ordinary and joined error chains without calling Error.
// It skips typed-nil errors before invoking Unwrap, since some standard error
// wrappers dereference their receiver there.
func findCause[T any](err error) (T, bool) {
	var found T
	matchedCause := false
	walkCauses(err, func(cause error) bool {
		matched, ok := cause.(T)
		if ok {
			found = matched
			matchedCause = true
			return true
		}
		return false
	})
	return found, matchedCause
}

func hasCause(err, target error) bool {
	return safeIs(err, target)
}

// safeIs preserves errors.Is semantics for ordinary wrappers. A malformed
// typed-nil wrapper can panic while Unwrap is called; in that case the cause
// supplies no cancellation/deadline fact and remains unknown.
func safeIs(err, target error) (matched bool) {
	defer func() {
		if recover() != nil {
			matched = false
		}
	}()
	return errors.Is(err, target)
}

func walkCauses(err error, visit func(error) bool) {
	pending := []error{err}
	for len(pending) > 0 {
		last := len(pending) - 1
		cause := pending[last]
		pending = pending[:last]
		if cause == nil || isNilError(cause) {
			continue
		}
		if visit(cause) {
			return
		}
		if joined, ok := cause.(interface{ Unwrap() []error }); ok {
			pending = append(pending, joined.Unwrap()...)
			continue
		}
		if wrapped, ok := cause.(interface{ Unwrap() error }); ok {
			pending = append(pending, wrapped.Unwrap())
		}
	}
}

func isNilError(err error) bool {
	v := reflect.ValueOf(err)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}

func Encode(err error, phase, operation string) string {
	return Classify(err, phase, operation).String()
}
func Safe(s string) bool { _, ok := AdmitStored(s); return ok }

// GuardErrorText is the final write-boundary guard for error-derived text.
// Absence remains absent; unsafe nonempty prose becomes a nonempty generic
// diagnostic so a failed disposition remains explainable without its cause.
func GuardErrorText(s string) string {
	if s == "" {
		return ""
	}
	if admitted, ok := AdmitStored(s); ok {
		return admitted
	}
	return Facts{Phase: "unknown", Operation: "unknown", Code: "unknown_error"}.String()
}

// GuardDisposition admits only the supplied field's closed enum values.
func GuardDisposition(s, field string) (string, bool) {
	if s == "" {
		return "", true
	}
	allowed := map[string]map[string]bool{
		"action_reason": set("already_completed prior_action_failed quota_exhausted mutation_application_uncertain authorization_pending"),
		"video_reason":  set("quota_exhausted api_rate_limited authorization_pending metadata_unavailable temporary_unavailable"),
		"queue_reason":  set("quota_exhausted api_rate_limited authorization_pending stopped_by_user discarded_by_user requeued_by_user"),
	}
	return s, allowed[field][s]
}

// AdmitStored accepts canonical v1 and exact historical literals only.
func AdmitStored(s string) (string, bool) {
	if s == "" {
		return "", true
	}
	if strings.HasPrefix(s, "diag:v1 ") {
		p := strings.Split(s, " ")
		if len(p) < 4 || p[0] != "diag:v1" {
			return "", false
		}
		f := Facts{}
		for _, part := range p[1:] {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				return "", false
			}
			switch kv[0] {
			case "phase":
				f.Phase = kv[1]
			case "operation":
				f.Operation = kv[1]
			case "code":
				f.Code = kv[1]
			case "status":
				f.Status = kv[1]
			case "reason":
				f.Reason = kv[1]
			case "sqlite":
				f.SQLite = kv[1]
			default:
				return "", false
			}
		}
		if f.String() != s {
			return "", false
		}
		return s, true
	}
	legacy := map[string]Facts{"discovery phase failed": {"discovery", "discover", "unknown_error", "", "", ""}, "action execution phase failed": {"action_execution", "execute_action", "unknown_error", "", "", ""}, "action execution failed": {"action_execution", "execute_action", "unknown_error", "", "", ""}, "playlist insertion failed": {"action_execution", "execute_action", "unknown_error", "", "", ""}, "playlist action failed": {"action_execution", "execute_action", "unknown_error", "", "", ""}, "retry failed": {"unknown", "retry", "unknown_error", "", "", ""}, "snapshot action authority failure": {"unknown", "execute_action", "action_authority_invalid", "", "", ""}, "run completion failed": {"unknown", "finalize_run", "unknown_error", "", "", ""}, "panic: unrecoverable error": {"unknown", "unknown", "panic", "", "", ""}}
	if f, ok := legacy[s]; ok {
		return f.String(), true
	}
	if strings.HasPrefix(s, "youtube API returned ") {
		parts := strings.SplitN(strings.TrimPrefix(s, "youtube API returned "), ": ", 2)
		status, err := strconv.Atoi(parts[0])
		if err == nil && strconv.Itoa(status) == parts[0] && status >= 300 && status <= 599 {
			f := Facts{Phase: "unknown", Operation: "unknown", Code: "provider_error", Status: parts[0]}
			if len(parts) == 2 {
				if !providerReasons[parts[1]] {
					return "", false
				}
				f.Reason = parts[1]
			}
			return f.String(), true
		}
	}
	return "", false
}

// AdmitErrorText normalizes recognized diagnostics and exact legacy forms.
// unavailable is true for rejected nonempty text and generic unknown_error
// summaries, whose cause detail cannot be shown.
func AdmitErrorText(s string) (normalized string, unavailable bool) {
	if s == "" {
		return "", false
	}
	value, ok := AdmitStored(s)
	if !ok {
		return "", true
	}
	fields := strings.Fields(value)
	return value, len(fields) > 3 && fields[3] == "code=unknown_error"
}

// AdmitPhase normalizes a recognized phase; unknown stored values are absent.
func AdmitPhase(s string) (string, bool) {
	if !phases[s] {
		return "", false
	}
	return s, true
}

// AdmitDisposition admits a field-specific registered outcome reason.
func AdmitDisposition(s, field string) (string, bool) {
	if s == "" {
		return "", true
	}
	return GuardDisposition(s, field)
}

func Public(s, fallback string) string {
	if s != "" {
		if v, ok := AdmitStored(s); ok {
			return v
		}
	}
	if v, ok := AdmitStored(fallback); ok {
		return v
	}
	return Facts{Phase: "unknown", Operation: "unknown", Code: "unknown_error"}.String()
}
func Reason(err error, fallback string) string {
	if err == nil {
		return Public(fallback, "")
	}
	if fallback == "snapshot action authority failure" {
		return (Facts{Phase: "unknown", Operation: "execute_action", Code: "action_authority_invalid"}).String()
	}
	phase, op := fallbackRoute(fallback)
	return Encode(err, phase, op)
}

// InterruptReason is the stored reason for a run whose execution context
// ended. The code comes from the context (canceled or timeout); phase and
// operation come from the failing step (carried by err or by the fallback
// literal), so the interruption stays explainable.
func InterruptReason(err, ctxErr error, fallback string) string {
	phase, op := fallbackRoute(fallback)
	at := Classify(err, phase, op)
	return Encode(ctxErr, at.Phase, at.Operation)
}

// fallbackRoute maps exact historical operation literals to trusted routing
// metadata.
func fallbackRoute(fallback string) (phase, op string) {
	phase, op = "unknown", "unknown"
	switch fallback {
	case "discovery phase failed":
		phase, op = "discovery", "discover"
	case "exclusion gate failed", "restore exclusion gate failed":
		phase, op = "exclusion", "apply_exclusions"
	case "duplicate detection phase failed":
		phase, op = "duplicate_detection", "detect_duplicates"
	case "routing phase failed":
		phase, op = "routing", "evaluate_video"
	case "action execution failed", "action execution phase failed", "playlist insertion failed", "playlist action failed":
		phase, op = "action_execution", "execute_action"
	case "manual retry failed":
		phase, op = "action_execution", "retry"
	case "retry failed", "retry processing failed":
		op = "retry"
	case "retry handler failed":
		op = "retry"
	case "load authority candidates", "list run videos failed", "load retry membership", "load matched videos failed", "load snapshot routes":
		op = "load_run"
	case "run completion failed":
		op = "finalize_run"
	}
	return phase, op
}
