package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
)

// Handler returns an http.Handler that serves the auth endpoints. The caller
// mounts this handler at a prefix such as /api/auth/.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /initiate", s.handleInitiate)
	mux.HandleFunc("GET /callback", s.handleCallback)
	return mux
}

// AccountHandler returns an http.Handler for account-level endpoints:
//
//	GET /status  -> connection status
//	DELETE /     -> disconnect account
func (s *Service) AccountHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("DELETE /", s.handleDisconnect)
	return mux
}

func (s *Service) handleInitiate(w http.ResponseWriter, r *http.Request) {
	resp, err := s.InitiateOAuth(r.Context())
	if err != nil {
		if errors.Is(err, ErrNotConfigured) {
			writeError(w, http.StatusServiceUnavailable, "youtube oauth is not configured")
			return
		}
		slog.ErrorContext(r.Context(), "oauth initiate failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to initiate authorization")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Service) handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	result, err := s.HandleOAuthCallback(r.Context(), code, state)
	if err != nil {
		if errors.Is(err, ErrNotConfigured) {
			if wantsHTML(r) {
				http.Redirect(w, r, "/settings?auth=error", http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusServiceUnavailable, "youtube oauth is not configured")
			return
		}
		slog.WarnContext(r.Context(), "oauth callback failed", "error", err)
		if wantsHTML(r) {
			http.Redirect(w, r, "/settings?auth=error", http.StatusSeeOther)
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.InfoContext(r.Context(), "oauth callback succeeded",
		"provider", result.Provider,
		"scopes", result.Scopes,
	)

	if wantsHTML(r) {
		http.Redirect(w, r, "/settings?auth=connected", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// wantsHTML reports whether the request is from a browser navigating the
// OAuth flow (Accept: text/html) rather than an API/XHR client.
func wantsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "text/html")
}

func (s *Service) handleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.Status(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "account status failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read account status")
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Service) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.Disconnect(r.Context()); err != nil {
		if errors.Is(err, ErrNotConfigured) {
			writeJSON(w, http.StatusOK, map[string]bool{"disconnected": true})
			return
		}
		slog.ErrorContext(r.Context(), "account disconnect failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to disconnect account")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"disconnected": true})
}
