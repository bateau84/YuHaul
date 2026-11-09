package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bateau84/YuHaul/internal/quota"
	"golang.org/x/oauth2"
)

// OAuthInitiateResponse is returned by POST /api/auth/initiate.
type OAuthInitiateResponse struct {
	AuthURL string `json:"auth_url"`
	State   string `json:"state"`
}

// OAuthCallbackResult is returned by GET /api/auth/callback on success.
type OAuthCallbackResult struct {
	Connected bool     `json:"connected"`
	Provider  string   `json:"provider"`
	Scopes    []string `json:"scopes"`
}

// InitiateOAuth begins the YouTube OAuth2 flow and returns the authorization
// URL the caller should redirect the user to.
func (s *Service) InitiateOAuth(ctx context.Context) (*OAuthInitiateResponse, error) {
	if !s.configured {
		return nil, ErrNotConfigured
	}
	s.mu.Lock()
	s.oauthGeneration++
	generation := s.oauthGeneration
	s.stateGenerations = nil
	s.mu.Unlock()
	state, err := generateState()
	if err != nil {
		return nil, err
	}

	// State lifetime matches common OAuth2 recommendation: short-lived,
	// single-use, bound to the initiation request.
	if err := s.states.Save(ctx, state, now().Add(10*time.Minute)); err != nil {
		return nil, fmt.Errorf("auth: failed to save state: %w", err)
	}
	s.mu.Lock()
	if s.stateGenerations == nil {
		s.stateGenerations = make(map[string]uint64)
	}
	s.stateGenerations[state] = generation
	s.mu.Unlock()

	authURL := s.oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	return &OAuthInitiateResponse{AuthURL: authURL, State: state}, nil
}

// HandleOAuthCallback processes the authorization code returned by Google.
// It validates the state parameter, exchanges the code for tokens, and stores
// the encrypted credentials.
func (s *Service) HandleOAuthCallback(ctx context.Context, code, state string) (*OAuthCallbackResult, error) {
	if !s.configured {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(code) == "" {
		return nil, errors.New("auth: authorization code is required")
	}
	if strings.TrimSpace(state) == "" {
		return nil, errors.New("auth: state is required")
	}

	valid, err := s.states.Validate(ctx, state)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to validate state: %w", err)
	}
	if !valid {
		return nil, errors.New("auth: invalid or expired state parameter")
	}

	s.mu.Lock()
	generation, initiated := s.stateGenerations[state]
	delete(s.stateGenerations, state)
	if !initiated || generation != s.oauthGeneration {
		s.mu.Unlock()
		return nil, ErrAuthorizationChanged
	}
	s.mu.Unlock()
	tokCtx := s.tokenContext(ctx)
	token, err := s.oauthConfig.Exchange(tokCtx, code)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to exchange authorization code: %w", err)
	}

	record := s.tokenToRecord(token)
	var expected *CredentialVersion
	current, err := s.store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to inspect current credentials: %w", err)
	}
	if current != nil {
		v := CredentialVersion{current.AuthorizationID, current.CredentialRevision}
		expected = &v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.oauthGeneration {
		return nil, ErrAuthorizationChanged
	}
	version, err := s.conditionalStore().ReplaceAuthorization(ctx, expected, record)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to save credentials: %w", err)
	}
	record.AuthorizationID, record.CredentialRevision = version.AuthorizationID, version.Revision
	s.authorityGeneration++
	s.pending = nil
	s.disconnected = false

	return &OAuthCallbackResult{
		Connected: true,
		Provider:  record.Provider,
		Scopes:    record.Scopes,
	}, nil
}

// Status returns the current connection state without exposing tokens.
func (s *Service) Status(ctx context.Context) (*ConnectionStatus, error) {
	if !s.configured {
		return &ConnectionStatus{
			Connected:           false,
			Authenticated:       false,
			Configured:          false,
			AuthorizationState:  "not_configured",
			AuthorizationReason: nil,
		}, nil
	}
	record, err := s.store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to load connection status: %w", err)
	}
	if record == nil {
		return &ConnectionStatus{
			Connected:           false,
			Authenticated:       false,
			Configured:          true,
			AuthorizationState:  "disconnected",
			AuthorizationReason: nil,
		}, nil
	}
	s.mu.Lock()
	if s.disconnected {
		s.mu.Unlock()
		return &ConnectionStatus{Configured: true, AuthorizationState: "disconnected"}, nil
	}
	renewing := s.flight != nil && s.flight.version.AuthorizationID == record.AuthorizationID && s.flight.version.Revision == record.CredentialRevision
	pending := s.pending != nil && s.pending.version == (CredentialVersion{record.AuthorizationID, record.CredentialRevision})
	knownFailure := s.knownFailure
	if knownFailure != nil && knownFailure.version != (CredentialVersion{record.AuthorizationID, record.CredentialRevision}) {
		knownFailure = nil
	}
	s.mu.Unlock()
	connected := tokenUsable(record) && !record.AccessRejected && record.RenewalFailure == ""
	state, reason := "renewal_available", (*string)(nil)
	switch {
	case renewing:
		state = "renewing"
	case record.RenewalFailure == "refresh_rejected":
		state, reason = "reconnect_required", stringPointer("refresh_rejected")
	case record.AccessRejected && record.RefreshToken == "":
		state, reason = "reconnect_required", stringPointer("refresh_rejected")
	case record.RefreshToken == "" && !connected:
		state, reason = "reconnect_required", stringPointer("refresh_missing")
	case record.RenewalFailure != "" || pending || knownFailure != nil:
		state = "renewal_unavailable"
		r := record.RenewalFailure
		if knownFailure != nil {
			r = knownFailure.reason
		}
		if r == "" {
			r = "persistence_failed"
		}
		reason = stringPointer(r)
	case connected:
		state = "usable"
	}
	return &ConnectionStatus{
		Connected:           connected && state == "usable",
		Authenticated:       connected && state == "usable",
		Configured:          true,
		AuthorizationState:  state,
		AuthorizationReason: reason,
		Provider:            record.Provider,
		Scopes:              record.Scopes,
		ConnectedAt:         record.ConnectedAt,
		ExpiresAt:           record.Expiry,
	}, nil
}

func stringPointer(value string) *string { return &value }

// Disconnect revokes the stored access token with Google (best-effort), then
// removes all stored credentials. Routing data, statistics, and configuration
// are not touched by this method.
func (s *Service) Disconnect(ctx context.Context) error {
	if !s.configured {
		return nil
	}
	s.mu.Lock()
	s.authorityGeneration++
	s.oauthGeneration++
	s.disconnected = true
	s.pending = nil
	s.knownFailure = nil
	generation := s.authorityGeneration
	s.mu.Unlock()
	record, err := s.store.Load(ctx)
	if err != nil {
		return fmt.Errorf("auth: failed to load credentials for disconnect: %w", err)
	}
	if record == nil {
		return nil
	}
	s.mu.Lock()
	if generation != s.authorityGeneration {
		s.mu.Unlock()
		return nil
	}
	version := CredentialVersion{record.AuthorizationID, record.CredentialRevision}
	if err := s.conditionalStore().DeleteIfCurrent(ctx, version); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("auth: failed to remove credentials: %w", err)
	}
	s.mu.Unlock()
	// Local deletion is authoritative. Revoke the captured token only after the
	// service lock is released so this network call cannot block new OAuth flow.
	_ = s.revokeToken(ctx, record.AccessToken)
	return nil
}

// revokeToken calls Google's OAuth2 revocation endpoint.
func (s *Service) revokeToken(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	form := url.Values{"token": {token}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/revoke", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := s.contextClient(ctx)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("revoke returned status %d", resp.StatusCode)
	}
	return nil
}

// ActiveToken returns the current OAuth2 token if the account is connected and
// the token is not expired. Callers should refresh using the refresh token if
// expired. This is intended for downstream YouTube API clients.
func (s *Service) ActiveToken(ctx context.Context) (*oauth2.Token, error) {
	return s.activeToken(ctx, "")
}

// AuthorizationBinding pins an invocation to one accepted OAuth connection.
// Its identity is deliberately opaque; it carries no credential or refresh capability.
type AuthorizationBinding struct {
	authorizationID string
}

// BindingIdentity returns the opaque, non-secret identity captured by b.
// It does not consult current account state or authorize a request.
func BindingIdentity(b AuthorizationBinding) (string, error) {
	if b.authorizationID == "" {
		return "", ErrAuthorizationChanged
	}
	return b.authorizationID, nil
}

// AdmissionReceipt identifies the authorization revision accepted for this
// immediate request. It is evidence, not a reusable sending permit.
type AdmissionReceipt struct {
	AuthorizationID    string
	CredentialRevision int64
	AdmittedAt         time.Time
}

// Dispatch resolves before admission, then fences the final check and request
// acceptance against Disconnect and account replacement. The fence is released
// before RoundTrip performs network I/O.
func (s *Service) Dispatch(ctx context.Context, b AuthorizationBinding, req *http.Request, transport http.RoundTripper) (*http.Response, AdmissionReceipt, error) {
	resp, receipt, _, err := s.dispatch(ctx, b, req, transport, nil, "")
	return resp, receipt, err
}

// DispatchWithQuota reserves and admits one attempt before sending. The caller
// must finalize the returned hold exactly once, including on transport errors.
// A nil hold means no attempt was admitted.
func (s *Service) DispatchWithQuota(ctx context.Context, b AuthorizationBinding, req *http.Request, transport http.RoundTripper, ledger *quota.Ledger, op quota.Operation) (*http.Response, AdmissionReceipt, *quota.Hold, error) {
	if ledger == nil {
		return nil, AdmissionReceipt{}, nil, errors.New("auth: nil quota ledger")
	}
	return s.dispatch(ctx, b, req, transport, ledger, op)
}

var ErrQuotaRefused = errors.New("auth: quota admission refused")
var ErrAccessExpiredBeforeAdmission = errors.New("auth: access expired before admission")

func (s *Service) dispatch(ctx context.Context, b AuthorizationBinding, req *http.Request, transport http.RoundTripper, ledger *quota.Ledger, op quota.Operation) (*http.Response, AdmissionReceipt, *quota.Hold, error) {
	if req == nil || req.URL == nil || transport == nil {
		return nil, AdmissionReceipt{}, nil, errors.New("auth: invalid dispatch request")
	}
	identity, err := BindingIdentity(b)
	if err != nil {
		return nil, AdmissionReceipt{}, nil, err
	}
	for {
		_, err := s.Resolve(ctx, b)
		if err != nil {
			return nil, AdmissionReceipt{}, nil, err
		}
		s.mu.Lock()
		if err := ctx.Err(); err != nil {
			s.mu.Unlock()
			return nil, AdmissionReceipt{}, nil, err
		}
		if s.disconnected {
			s.mu.Unlock()
			return nil, AdmissionReceipt{}, nil, ErrAuthorizationChanged
		}
		current, err := s.store.Load(ctx)
		if err != nil {
			s.mu.Unlock()
			return nil, AdmissionReceipt{}, nil, fmt.Errorf("auth: failed to recheck dispatch authority: %w", err)
		}
		if current == nil || current.AuthorizationID != identity {
			s.mu.Unlock()
			return nil, AdmissionReceipt{}, nil, ErrAuthorizationChanged
		}
		known := s.knownFailure != nil && s.knownFailure.version == (CredentialVersion{current.AuthorizationID, current.CredentialRevision})
		if !tokenUsable(current) || current.AccessRejected || current.RenewalFailure != "" || known {
			s.mu.Unlock()
			continue
		}
		// Resolve may have returned a stale revision across a concurrent renewal.
		// The current durable access is authoritative; install it only under fence.
		out := req.Clone(ctx)
		out.Header = req.Header.Clone()
		out.Header.Set("Authorization", "Bearer "+current.AccessToken)
		admittedAt := now().UTC()
		var hold *quota.Hold
		if ledger != nil {
			hold, err = ledger.Acquire(ctx, op)
			if err == nil && hold == nil {
				err = ErrQuotaRefused
			}
			if err == nil {
				err = hold.AdmitIf(ctx, func(at time.Time) error {
					if !current.Expiry.After(at.Add(10 * time.Second)) {
						return ErrAccessExpiredBeforeAdmission
					}
					return nil
				})
			}
			if err != nil {
				if hold != nil {
					err = errors.Join(err, hold.Cancel())
				}
				s.mu.Unlock()
				return nil, AdmissionReceipt{}, nil, err
			}
			admittedAt = hold.AdmissionTime()
		}
		receipt := AdmissionReceipt{AuthorizationID: identity, CredentialRevision: current.CredentialRevision, AdmittedAt: admittedAt}
		s.mu.Unlock()
		resp, err := transport.RoundTrip(out)
		return resp, receipt, hold, err
	}
}

// Bind captures the currently stored authorization identity without renewing it.
func (s *Service) Bind(ctx context.Context) (AuthorizationBinding, error) {
	if !s.configured {
		return AuthorizationBinding{}, ErrNotConfigured
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.disconnected {
		return AuthorizationBinding{}, errors.New("auth: no connected account")
	}
	record, err := s.store.Load(ctx)
	if err != nil {
		return AuthorizationBinding{}, fmt.Errorf("auth: failed to bind authorization: %w", err)
	}
	if record == nil {
		return AuthorizationBinding{}, errors.New("auth: no connected account")
	}
	return AuthorizationBinding{authorizationID: record.AuthorizationID}, nil
}

// Resolve returns current credentials for b's identity, renewing them if needed.
// It never follows a later connection accepted for a different identity.
func (s *Service) Resolve(ctx context.Context, b AuthorizationBinding) (*oauth2.Token, error) {
	if b.authorizationID == "" {
		return nil, ErrAuthorizationChanged
	}
	return s.activeToken(ctx, b.authorizationID)
}

// ObserveAccessRejection fences only the exact credential revision used by an
// admitted request, then resolves that binding so later requests can use a
// renewed credential. It never retries the request that produced the result.
func (s *Service) ObserveAccessRejection(ctx context.Context, b AuthorizationBinding, receipt AdmissionReceipt) error {
	identity, err := BindingIdentity(b)
	if err != nil || identity != receipt.AuthorizationID || receipt.CredentialRevision <= 0 || receipt.AdmittedAt.IsZero() {
		return ErrAuthorizationChanged
	}
	s.mu.Lock()
	if s.disconnected {
		s.mu.Unlock()
		return ErrAuthorizationChanged
	}
	current, err := s.store.Load(ctx)
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("auth: failed to inspect rejected credential: %w", err)
	}
	if current == nil || current.AuthorizationID != identity || current.CredentialRevision != receipt.CredentialRevision {
		s.mu.Unlock()
		return nil // A late rejection cannot invalidate newer credentials.
	}
	version := CredentialVersion{AuthorizationID: identity, Revision: receipt.CredentialRevision}
	if err := s.conditionalStore().SaveRenewalFailure(ctx, version, "access_rejected"); err != nil {
		s.knownFailure = &knownRenewalFailure{version: version, reason: "persistence_failed"}
		s.mu.Unlock()
		return ErrRenewalPersistence
	}
	s.knownFailure = &knownRenewalFailure{version: version, reason: "access_rejected"}
	s.mu.Unlock()
	_, err = s.Resolve(ctx, b)
	return err
}

func (s *Service) activeToken(ctx context.Context, boundAuthorizationID string) (*oauth2.Token, error) {
	if !s.configured {
		return nil, ErrNotConfigured
	}
	record, err := s.store.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: failed to load token: %w", err)
	}
	if record == nil {
		if boundAuthorizationID != "" {
			return nil, ErrAuthorizationChanged
		}
		return nil, errors.New("auth: no connected account")
	}
	if boundAuthorizationID != "" && record.AuthorizationID != boundAuthorizationID {
		return nil, ErrAuthorizationChanged
	}
	s.mu.Lock()
	if s.disconnected {
		s.mu.Unlock()
		if boundAuthorizationID != "" {
			return nil, ErrAuthorizationChanged
		}
		return nil, errors.New("auth: no connected account")
	}
	current, loadErr := s.store.Load(ctx)
	if loadErr != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("auth: failed to recheck token authority: %w", loadErr)
	}
	if current == nil || current.AuthorizationID != record.AuthorizationID || (boundAuthorizationID != "" && current.AuthorizationID != boundAuthorizationID) {
		s.mu.Unlock()
		return nil, ErrAuthorizationChanged
	}
	if current.CredentialRevision != record.CredentialRevision {
		if tokenUsable(current) && !current.AccessRejected && current.RenewalFailure == "" {
			s.mu.Unlock()
			return recordToToken(current), nil
		}
		s.mu.Unlock()
		return nil, ErrAuthorizationChanged
	}
	record = current
	if s.knownFailure != nil && s.knownFailure.version == (CredentialVersion{record.AuthorizationID, record.CredentialRevision}) {
		// A retained rotated candidate for this exact version is retried below
		// (ARS-001) before any new exchange, so a transient save failure heals
		// without a restart. Without a candidate the failure stays authoritative.
		hasPending := s.pending != nil && s.pending.version == (CredentialVersion{record.AuthorizationID, record.CredentialRevision})
		if s.knownFailure.reason == "persistence_failed" && !hasPending {
			s.mu.Unlock()
			return nil, ErrRenewalPersistence
		}
		if s.knownFailure.reason == "access_rejected" {
			record.AccessRejected = true
		}
	}
	if tokenUsable(record) && !record.AccessRejected {
		s.mu.Unlock()
		return recordToToken(record), nil
	}
	if record.RefreshToken == "" {
		s.mu.Unlock()
		return nil, errors.New("auth: refresh authorization is unavailable")
	}
	version := CredentialVersion{record.AuthorizationID, record.CredentialRevision}
	// The authority check above applies to every return path, including usable tokens.
	if pending := s.pending; pending != nil && pending.version == version {
		candidate := pending.record
		newVersion, saveErr := s.conditionalStore().SaveRefresh(ctx, version, &candidate)
		if saveErr == nil {
			s.pending = nil
			s.knownFailure = nil
			candidate.CredentialRevision = newVersion.Revision
			if candidate.Expiry.After(now().Add(10 * time.Second)) {
				s.mu.Unlock()
				return recordToToken(&candidate), nil
			}
		}
		if saveErr != nil {
			s.mu.Unlock()
			if ctxErr := ctx.Err(); ctxErr != nil {
				// The candidate stays pending; a cancelled caller is not a
				// persistence verdict.
				return nil, fmt.Errorf("auth: renewal interrupted: %w", ctxErr)
			}
			return nil, ErrRenewalPersistence
		}
		// The rotated refresh credential is now durable, but its access token
		// is no longer usable. Resolve again from that committed snapshot.
		record = &candidate
		version = newVersion
	}
	if f := s.flight; f != nil && f.version == version {
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.done:
		}
		if f.err != nil {
			return nil, f.err
		}
		return cloneToken(f.token), nil
	}
	f := &renewalFlight{done: make(chan struct{}), version: version}
	s.flight = f
	generation := s.authorityGeneration
	s.mu.Unlock()
	tokCtx, cancel := context.WithTimeout(s.tokenContext(ctx), 30*time.Second)
	defer cancel()
	refreshInput := recordToToken(record)
	if !tokenUsable(record) || record.AccessRejected {
		// oauth2's reuse-token source treats zero expiry as non-expiring and
		// otherwise reuses an unexpired token. Expire only this copy to force
		// renewal whenever our authorization validity check rejects it.
		refreshInput.Expiry = now().Add(-time.Second)
	}
	refreshed, err := s.oauthConfig.TokenSource(tokCtx, refreshInput).Token()
	failure := ""
	if err == nil && (refreshed.AccessToken == "" || !strings.EqualFold(refreshed.TokenType, "Bearer") || refreshed.Expiry.IsZero() || !refreshed.Expiry.After(now().Add(10*time.Second)) || !refreshScopesCompatible(refreshed, record.Scopes)) {
		err = errors.New("auth: refresh returned unusable credentials")
		failure = "invalid_response"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.authorityGeneration {
		err = ErrAuthorizationChanged
	}
	if err == nil {
		updated := *record
		updated.AccessToken, updated.TokenType, updated.Expiry = refreshed.AccessToken, "Bearer", refreshed.Expiry
		if refreshed.RefreshToken != "" {
			updated.RefreshToken = refreshed.RefreshToken
		}
		updated.UpdatedAt = now()
		f.version, err = s.conditionalStore().SaveRefresh(ctx, version, &updated)
		if err == nil {
			f.token = recordToToken(&updated)
			s.knownFailure = nil
		} else {
			// The rotated credential must survive whatever the cause.
			s.pending = &pendingRenewal{version: version, record: *cloneRecord(&updated)}
			if ctxErr := ctx.Err(); ctxErr != nil {
				// Cancelled mid-save: not a store verdict. Record no failure
				// (it could not be written under this context anyway) and let
				// the next call retry the pending candidate.
				err = fmt.Errorf("auth: renewal interrupted: %w", ctxErr)
			} else {
				err = ErrRenewalPersistence
				failure = "persistence_failed"
			}
		}
	} else if !errors.Is(err, ErrAuthorizationChanged) {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// x/oauth2 flattens transport errors with %v, so cancellation is only
			// visible on the caller's context. A cancelled request is not a
			// provider verdict: persist nothing and leave no sticky failure.
			err = fmt.Errorf("auth: renewal interrupted: %w", ctxErr)
		} else {
			failure = classifyRenewalFailure(err)
		}
	}
	if failure != "" && generation == s.authorityGeneration {
		// Conditional persistence prevents a late OAuth result from modifying a
		// replacement connection. The returned error is deliberately safe and
		// never carries RetrieveError response/body/description fields.
		if saveErr := s.conditionalStore().SaveRenewalFailure(ctx, version, failure); saveErr != nil {
			if !errors.Is(saveErr, ErrAuthorizationChanged) {
				failure = "persistence_failed"
				s.knownFailure = &knownRenewalFailure{version: version, reason: failure}
			}
		} else {
			s.knownFailure = nil
		}
		if failure == "refresh_rejected" {
			err = errRefreshRejected
		} else if failure == "client_configuration" {
			err = errClientConfiguration
		} else if failure == "temporary_network" {
			err = errTemporaryNetwork
		} else if failure == "temporary_provider" {
			err = errTemporaryProvider
		} else if failure == "persistence_failed" {
			err = ErrRenewalPersistence
		} else {
			err = errInvalidRefreshResponse
		}
	}
	f.err = err
	if s.flight == f {
		s.flight = nil
	}
	close(f.done)
	if err != nil {
		return nil, err
	}
	return cloneToken(f.token), nil
}

// tokenUsable is the shared authorization-validity boundary for status and
// dispatch. Zero-expiry credentials are not usable.
func tokenUsable(record *TokenRecord) bool {
	return record != nil && !record.Expiry.IsZero() && record.Expiry.After(now().Add(10*time.Second))
}

var ErrRenewalPersistence = errors.New("auth: renewal credentials could not be persisted")

var (
	errRefreshRejected        = errors.New("auth: refresh authorization was rejected")
	errTemporaryNetwork       = errors.New("auth: temporary renewal network failure")
	errTemporaryProvider      = errors.New("auth: temporary renewal provider failure")
	errClientConfiguration    = errors.New("auth: OAuth client configuration is invalid")
	errInvalidRefreshResponse = errors.New("auth: renewal response is invalid")
)

// RenewalFailureCategory exposes only the registered recovery classification.
func RenewalFailureCategory(err error) string {
	switch {
	case errors.Is(err, errRefreshRejected):
		return "refresh_rejected"
	case errors.Is(err, errTemporaryNetwork):
		return "temporary_network"
	case errors.Is(err, errTemporaryProvider):
		return "temporary_provider"
	case errors.Is(err, errClientConfiguration):
		return "client_configuration"
	case errors.Is(err, errInvalidRefreshResponse):
		return "invalid_response"
	case errors.Is(err, ErrRenewalPersistence):
		return "persistence_failed"
	case errors.Is(err, ErrAuthorizationChanged):
		return "authorization_changed"
	default:
		return "unknown"
	}
}

// IsPreAdmissionAuthorizationRefusal reports only authorization conditions
// that positively prevent Dispatch from admitting the protected request.
// It intentionally excludes cancellation, persistence, configuration, and
// malformed-renewal failures.
func IsPreAdmissionAuthorizationRefusal(err error) bool {
	if errors.Is(err, context.Canceled) {
		return false
	}
	return errors.Is(err, ErrAuthorizationChanged) ||
		errors.Is(err, ErrAccessExpiredBeforeAdmission) ||
		errors.Is(err, errRefreshRejected) ||
		errors.Is(err, errTemporaryNetwork) ||
		errors.Is(err, errTemporaryProvider)
}

func classifyRenewalFailure(err error) string {
	var retrieve *oauth2.RetrieveError
	if errors.As(err, &retrieve) {
		switch retrieve.ErrorCode {
		case "invalid_grant":
			return "refresh_rejected"
		case "invalid_client", "unauthorized_client", "invalid_scope", "unsupported_grant_type":
			return "client_configuration"
		}
		if retrieve.Response != nil && (retrieve.Response.StatusCode == http.StatusTooManyRequests || retrieve.Response.StatusCode >= 500) {
			return "temporary_provider"
		}
		return "invalid_response"
	}
	var netErr net.Error
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) {
		return "temporary_network"
	}
	return "invalid_response"
}

func refreshScopesCompatible(token *oauth2.Token, prior []string) bool {
	value := token.Extra("scope")
	if value == nil || value == "" {
		return true
	}
	returned, ok := value.(string)
	if !ok {
		return false
	}
	allowed := make(map[string]struct{}, len(prior))
	for _, scope := range prior {
		allowed[scope] = struct{}{}
	}
	for _, scope := range strings.Fields(returned) {
		if _, ok := allowed[scope]; !ok {
			return false
		}
	}
	return true
}

func cloneRecord(record *TokenRecord) *TokenRecord {
	if record == nil {
		return nil
	}
	copy := *record
	copy.Scopes = append([]string(nil), record.Scopes...)
	return &copy
}

func (s *Service) conditionalStore() ConditionalTokenStore {
	store, ok := s.store.(ConditionalTokenStore)
	if !ok {
		panic("auth: TokenStore does not support conditional credential operations")
	}
	return store
}

func cloneToken(token *oauth2.Token) *oauth2.Token {
	if token == nil {
		return nil
	}
	copy := *token
	return &copy
}

// tokenToRecord converts an oauth2.Token into our durable record format.
func (s *Service) tokenToRecord(token *oauth2.Token) *TokenRecord {
	t := now()
	return &TokenRecord{
		Provider:    "youtube",
		AccessToken: token.AccessToken,
		RefreshToken: func() string {
			if token.RefreshToken != "" {
				return token.RefreshToken
			}
			return ""
		}(),
		TokenType:   token.TokenType,
		Expiry:      token.Expiry,
		Scopes:      append([]string(nil), s.oauthConfig.Scopes...),
		ConnectedAt: t,
		UpdatedAt:   t,
	}
}

// recordToToken converts a TokenRecord back to an oauth2.Token.
func recordToToken(record *TokenRecord) *oauth2.Token {
	return &oauth2.Token{
		AccessToken:  record.AccessToken,
		RefreshToken: record.RefreshToken,
		TokenType:    record.TokenType,
		Expiry:       record.Expiry,
	}
}

func isExpired(expiry time.Time) bool {
	if expiry.IsZero() {
		return false // no expiry means no expiration
	}
	return now().After(expiry)
}

// writeJSON writes v as JSON with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Headers already sent; nothing useful to do here except log.
		_ = err
	}
}

// writeError writes a structured error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
