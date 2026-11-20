package youtube

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// PlaylistClaim is the immutable descriptor committed by the production
// executor before dispatch. The adapter checks it is complete and never
// substitutes its target or authorization identity.
type PlaylistClaim struct {
	RunID            string
	VideoID          string
	RouteDecisionID  string
	ActionType       string
	TargetPlaylistID string
	AuthorizationID  string
}

// MutationApplication states what the adapter can establish about one send.
type MutationApplication string

const (
	MutationNotIssued  MutationApplication = "not_issued"
	MutationSucceeded  MutationApplication = "succeeded"
	MutationNotApplied MutationApplication = "not_applied"
	MutationUncertain  MutationApplication = "uncertain"
)

// DispatchAdmission is copied from auth.Dispatch; it is evidence, not a
// reusable sending permit. Keep the adapter independent of the auth package.
type DispatchAdmission struct {
	AuthorizationID    string
	CredentialRevision int64
	AdmittedAt         time.Time
}

// MutationResult keeps application facts separate from auxiliary accounting
// failure. Cause is a bounded API/transport error, never a raw provider body.
type MutationResult struct {
	Application        MutationApplication
	Claim              PlaylistClaim
	Admission          *DispatchAdmission
	Refusal            DispatchRefusalKind
	Cause              error
	Accounting         error
	Renewal            error
	HTTPStatus         int // Zero means no response was received.
	AccountingCategory string
	AccessRejected     bool
}

// RejectionObserver reports a typed rejection for the exact dispatched
// credential. It is recovery for later requests, never a mutation retry.
type RejectionObserver func(context.Context, DispatchAdmission) error

// DispatchFunc must synchronously pass the request to auth.Service.Dispatch
// and return its admission receipt even when the admitted transport fails.
type DispatchFunc func(context.Context, *http.Request, http.RoundTripper) (*http.Response, DispatchAdmission, bool, error)

// DispatchRefusalKind classifies a positively identified pre-admission refusal.
// Unknown is deliberately not authorization refusal: a missing receipt alone
// cannot establish why dispatch did not admit the request.
type DispatchRefusalKind string

const (
	DispatchRefusalUnknown         DispatchRefusalKind = "unknown"
	DispatchRefusalAuthorization   DispatchRefusalKind = "authorization"
	DispatchRefusalLocalValidation DispatchRefusalKind = "local_validation"
	DispatchRefusalQuota           DispatchRefusalKind = "quota"
	// DispatchRefusalCanceled marks a caller context that ended before
	// admission (ADR-007 §3.2). Dispatch admitted no request, so the claiming
	// invocation may release its own guard.
	DispatchRefusalCanceled DispatchRefusalKind = "canceled"
)

// DispatchRefusalError carries a bounded classification across the auth
// adapter boundary without exposing the underlying error to diagnostics.
type DispatchRefusalError struct {
	Kind DispatchRefusalKind
}

func (e DispatchRefusalError) Error() string { return "dispatch refused before admission" }

// InsertGuardedPlaylistItem sends exactly one insertion for claim and returns
// explicit application/admission facts. It never retries or follows redirects.
// The legacy observer argument is ignored; the executor must account before observing rejection.
func (c *Client) InsertGuardedPlaylistItem(ctx context.Context, claim PlaylistClaim, externalVideoID string, dispatch DispatchFunc, _ RejectionObserver) MutationResult {
	result := MutationResult{Application: MutationNotIssued, Claim: claim, AccountingCategory: "transient"}
	if c.transportErr != nil {
		result.Cause = c.transportErr
		return result
	}
	if claim.RunID == "" || claim.VideoID == "" || claim.RouteDecisionID == "" || claim.ActionType != "playlist_insert" || claim.TargetPlaylistID == "" || claim.AuthorizationID == "" || externalVideoID == "" || dispatch == nil {
		result.Cause = fmt.Errorf("invalid guarded playlist insertion")
		return result
	}
	payload, err := json.Marshal(map[string]any{"snippet": map[string]any{"playlistId": claim.TargetPlaylistID, "resourceId": map[string]string{"kind": "youtube#video", "videoId": externalVideoID}}})
	if err != nil {
		result.Cause = fmt.Errorf("build playlist insertion: %w", err)
		return result
	}
	u, err := url.Parse(c.baseURL + "/playlistItems")
	if err != nil {
		result.Cause = fmt.Errorf("build playlist insertion: %w", boundedTransportError(err))
		return result
	}
	u.RawQuery = url.Values{"part": {"snippet"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		result.Cause = fmt.Errorf("build playlist insertion: %w", boundedTransportError(err))
		return result
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	transport := c.httpClient.Transport
	// Guarded requests are authorized only by Dispatch. Bypass the ordinary
	// per-request authorization wrapper here; otherwise it would resolve and
	// install a token before the dispatch fence, creating a second authority
	// check outside the actual admission boundary.
	if authorized, ok := transport.(authorizationTransport); ok {
		transport = authorized.base
	}
	if transport == nil {
		transport = http.DefaultTransport
	}
	resp, admission, notAdmitted, err := dispatch(ctx, req, transport)
	if admission.AuthorizationID != "" {
		result.Admission = &admission
	}
	if resp != nil {
		result.HTTPStatus = resp.StatusCode
		if resp.Body != nil {
			defer resp.Body.Close()
		}
	}
	if err != nil {
		result.Cause = boundedRequestError(err)
		result.AccountingCategory = readCategory(result.HTTPStatus, nil)
		result.AccessRejected = result.HTTPStatus == http.StatusUnauthorized && validAdmission(result.Admission, claim)
		if !notAdmitted || result.Admission != nil {
			result.Application = MutationUncertain
		} else {
			var refusal DispatchRefusalError
			if errors.As(err, &refusal) && (refusal.Kind == DispatchRefusalAuthorization || refusal.Kind == DispatchRefusalLocalValidation || refusal.Kind == DispatchRefusalQuota || refusal.Kind == DispatchRefusalCanceled) {
				result.Refusal = refusal.Kind
			}
		}
		return result
	}
	if notAdmitted {
		result.Application = MutationNotIssued
		result.Cause = fmt.Errorf("dispatch refused before admission")
		var refusal DispatchRefusalError
		if errors.As(err, &refusal) && (refusal.Kind == DispatchRefusalAuthorization || refusal.Kind == DispatchRefusalLocalValidation || refusal.Kind == DispatchRefusalQuota) {
			result.Refusal = refusal.Kind
		}
		return result
	}
	if resp == nil {
		result.Application = MutationUncertain
		result.Cause = fmt.Errorf("missing provider response")
		return result
	}
	if resp.Body == nil {
		result.Application = MutationUncertain
		result.AccountingCategory = readCategory(resp.StatusCode, nil)
		result.AccessRejected = resp.StatusCode == http.StatusUnauthorized && validAdmission(result.Admission, claim)
		result.Cause = fmt.Errorf("missing provider response body")
		return result
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		result.AccountingCategory = readCategory(resp.StatusCode, nil)
		result.AccessRejected = resp.StatusCode == http.StatusUnauthorized && validAdmission(result.Admission, claim)
		result.Application = MutationUncertain
		result.Cause = fmt.Errorf("read playlist insertion response: %w", boundedTransportError(err))
		return result
	}
	var responseErr error
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseErr = youtubeAPIError(resp.StatusCode, body)
	}
	result.AccountingCategory = readCategory(resp.StatusCode, responseErr)
	if !validAdmission(result.Admission, claim) {
		result.Application = MutationUncertain
		result.Cause = fmt.Errorf("missing or mismatched dispatch admission")
		return result
	}
	if resp.StatusCode == http.StatusForbidden {
		apiErr := youtubeAPIError(resp.StatusCode, body)
		var api *APIError
		if errors.As(apiErr, &api) && api.Complete && len(api.Reasons) == 1 && api.Reasons[0] == "quotaExceeded" {
			result.Application = MutationNotApplied
			result.Cause = apiErr
			return result
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Application = MutationUncertain
		result.Cause = responseErr
		var api *APIError
		result.AccessRejected = resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden && errors.As(responseErr, &api) && api.Complete && len(api.Reasons) == 1 && api.Reasons[0] == "authError"
		return result
	}
	var inserted struct {
		ID      string `json:"id"`
		Snippet struct {
			PlaylistID string `json:"playlistId"`
			ResourceID struct {
				VideoID string `json:"videoId"`
			} `json:"resourceId"`
		} `json:"snippet"`
	}
	if err := json.Unmarshal(body, &inserted); err != nil || inserted.ID == "" || inserted.Snippet.PlaylistID != claim.TargetPlaylistID || inserted.Snippet.ResourceID.VideoID != externalVideoID {
		result.Application = MutationUncertain
		result.Cause = invalidResponseError(fmt.Errorf("incomplete playlist insertion response"), OpPlaylistItemsInsert)
		return result
	}
	result.Application = MutationSucceeded
	return result
}

func validAdmission(admission *DispatchAdmission, claim PlaylistClaim) bool {
	return admission != nil && admission.AuthorizationID == claim.AuthorizationID && admission.CredentialRevision > 0 && !admission.AdmittedAt.IsZero()
}
