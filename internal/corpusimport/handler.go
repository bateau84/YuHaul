package corpusimport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"

	"github.com/bateau84/YuHaul/internal/migration"
)

// errInvalidUpload marks malformed multipart framing (wrong content type,
// unparseable body, a non-file part, a part not named "file", or more than one
// file part). It is always a client error (HTTP 400) and never echoes
// request contents.
var errInvalidUpload = errors.New("corpusimport: invalid upload request")

// Handler exposes the ARS-002 corpus import HTTP contract. It is mounted by
// internal/server under /api/corpus/ and holds no auth of its own: the
// selected access-control boundary is the Traefik BasicAuth Ingress
// (ADR-010/ARS-003), and direct port access is operator-only.
type Handler struct {
	manager *Manager
	logger  *slog.Logger
}

// NewHandler returns the corpus import HTTP handler bound to a manager.
func NewHandler(m *Manager, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{manager: m, logger: logger}
}

// Handler returns the HTTP handler for the corpus import routes.
func (h *Handler) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/corpus/import/preview", h.handlePreview)
	mux.HandleFunc("POST /api/corpus/import", h.handleStart)
	mux.HandleFunc("POST /api/corpus/import/{$}", h.handleStart)
	mux.HandleFunc("GET /api/corpus/import", h.handleLatest)
	mux.HandleFunc("GET /api/corpus/import/{$}", h.handleLatest)
	// The literal /metadata route is reserved (ARS-002): the more specific
	// literal pattern wins over GET /api/corpus/import/{id} below, so it is
	// never interpreted as an import ID.
	mux.HandleFunc("GET /api/corpus/import/metadata", h.handleMetadata)
	mux.HandleFunc("GET /api/corpus/import/{id}", h.handleStatus)
	mux.HandleFunc("POST /api/corpus/import/{id}/cancel", h.handleCancel)
	return mux
}

// handlePreview implements POST /api/corpus/import/preview: it stages the
// upload privately, validates and reads it read-only, returns the preview, and
// removes the staged file. It never writes to the destination (ARS-002).
func (h *Handler) handlePreview(w http.ResponseWriter, r *http.Request) {
	staged, err := h.stageUpload(w, r)
	if err != nil {
		h.writeFailure(w, r, "preview", err)
		return
	}
	// The preview removes the staged source on every path, including failure.
	defer h.manager.removeStaged(staged)

	result, err := migration.PreviewStaged(r.Context(), staged, h.manager.dest)
	if err != nil {
		h.writeFailure(w, r, "preview", err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleStart implements POST /api/corpus/import: it stages the upload
// privately, revalidates it, and accepts one append-only import job (202 with
// an import ID). A second start while one is active is 409.
func (h *Handler) handleStart(w http.ResponseWriter, r *http.Request) {
	staged, err := h.stageUpload(w, r)
	if err != nil {
		h.writeFailure(w, r, "start", err)
		return
	}
	// startStaged owns the staged path from here: it removes it on every
	// rejection and the accepted job removes it at termination.
	status, err := h.manager.startStaged(r.Context(), staged)
	if err != nil {
		h.writeFailure(w, r, "start", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{
		"import_id": status.ImportID,
		"status":    StatusQueued,
	})
}

// handleStatus implements GET /api/corpus/import/{import_id}. An unknown ID —
// including a job lost to a process restart — is 404.
func (h *Handler) handleStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.manager.Status(r.PathValue("id"))
	if err != nil {
		h.writeFailure(w, r, "status", err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleLatest implements GET /api/corpus/import: the active operation or the
// most recent terminal one retained by this process; 404 when the process
// holds no import state (ARS-002; the Settings view treats that as an unknown
// prior outcome).
func (h *Handler) handleLatest(w http.ResponseWriter, r *http.Request) {
	status, err := h.manager.Latest()
	if err != nil {
		h.writeFailure(w, r, "latest", err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// handleMetadata implements GET /api/corpus/import/metadata: the durable
// completion time of the most recent successful import, or null when none is
// recorded. It reads the database directly and is independent of the
// process-local job manager, so it returns the prior value after a restart
// (ADR-015). A read failure is 500; it never replaces the stored value.
func (h *Handler) handleMetadata(w http.ResponseWriter, r *http.Request) {
	meta, err := h.manager.ImportMetadata(r.Context())
	if err != nil {
		h.logger.WarnContext(r.Context(), "corpus import metadata read failed",
			"error", h.manager.sanitize(err))
		writeJSON(w, http.StatusInternalServerError,
			map[string]string{"error": "failed to read corpus import metadata"})
		return
	}
	writeJSON(w, http.StatusOK, meta)
}

// handleCancel implements POST /api/corpus/import/{import_id}/cancel:
// 202 + cancel_requested for an active job, 200 + the unchanged terminal
// state for a terminal job, 404 for an unknown ID.
func (h *Handler) handleCancel(w http.ResponseWriter, r *http.Request) {
	status, err := h.manager.Cancel(r.PathValue("id"))
	if err != nil {
		h.writeFailure(w, r, "cancel", err)
		return
	}
	code := http.StatusAccepted
	if status.IsTerminal() {
		code = http.StatusOK
	}
	writeJSON(w, code, status)
}

// stageUpload streams the request's single file part named "file" into the
// private staging area. The total request body is bounded by the configured
// file cap plus MultipartOverheadBytes of multipart framing (ARS-002), and the
// stager enforces the file cap itself. Any other multipart part is rejected.
// On success the caller owns the returned staged path.
func (h *Handler) stageUpload(w http.ResponseWriter, r *http.Request) (string, error) {
	if _, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil {
		return "", fmt.Errorf("%w: missing or malformed content type", errInvalidUpload)
	}
	// Bound the total request body by the file cap plus 1 MiB of multipart
	// framing (ARS-002). A known over-limit Content-Length is rejected before
	// any body is read; the MaxBytesReader below enforces the same bound for
	// unknown-length or lying requests.
	envelope := h.manager.maxUploadBytes + MultipartOverheadBytes
	if r.ContentLength > envelope {
		return "", migration.ErrUploadTooLarge
	}
	r.Body = http.MaxBytesReader(w, r.Body, envelope)

	mr, err := r.MultipartReader()
	if err != nil {
		return "", fmt.Errorf("%w: %v", errInvalidUpload, err)
	}

	var staged string
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// A body-limit breach stays 413; any other framing failure
			// (truncated or malformed multipart) is a client error.
			var maxBytesErr *http.MaxBytesError
			if !errors.As(err, &maxBytesErr) {
				err = fmt.Errorf("%w: malformed multipart body", errInvalidUpload)
			}
			if staged != "" {
				h.manager.removeStaged(staged)
			}
			return "", err
		}
		if part.FormName() != "file" || part.FileName() == "" || staged != "" {
			// A non-file part, a form field named "file", or a second file
			// part: the contract allows exactly one file part named "file".
			_ = part.Close()
			if staged != "" {
				h.manager.removeStaged(staged)
			}
			return "", fmt.Errorf("%w: expected exactly one file part named file", errInvalidUpload)
		}
		staged, err = h.manager.stager.Stage(r.Context(), part)
		_ = part.Close()
		if err != nil {
			return "", err
		}
	}
	if staged == "" {
		return "", fmt.Errorf("%w: missing file part", errInvalidUpload)
	}
	return staged, nil
}

// writeFailure maps an operation failure to the ARS-002 HTTP contract with
// generic client-safe messages (no titles, contents, paths, SQL, or stack
// traces), and logs the sanitized internal cause.
func (h *Handler) writeFailure(w http.ResponseWriter, r *http.Request, op string, err error) {
	h.logger.WarnContext(r.Context(), "corpus import request failed",
		"operation", op, "error", h.manager.sanitize(err))

	var maxBytesErr *http.MaxBytesError
	switch {
	case errors.As(err, &maxBytesErr), errors.Is(err, migration.ErrUploadTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge,
			map[string]string{"error": "upload exceeds the configured maximum size"})
	case errors.Is(err, errInvalidUpload):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid upload request"})
	case errors.Is(err, migration.ErrUploadEmpty),
		errors.Is(err, migration.ErrInvalidSource),
		errors.Is(err, migration.ErrSchemaNotDiscoverable):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported or invalid source database"})
	case errors.Is(err, ErrImportActive):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "an import is already active"})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "import not found"})
	case errors.Is(err, ErrNoState):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no import state available"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("corpus import failed to encode response", "error", err)
	}
}
