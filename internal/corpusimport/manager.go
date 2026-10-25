// Package corpusimport implements the process-local corpus import job manager
// and its HTTP API (ARS-002, ADR-009).
//
// The Settings UI uploads the prior application's SQLite corpus, previews it,
// and confirms an append-only import. The manager accepts at most one active
// import, runs the bounded-commit importer (migration.Importer.RunChunked) in
// a background goroutine, and keeps the active job plus its most recent
// terminal result in memory only. There is no durable job history: a process
// restart loses job status while committed fingerprints remain in SQLite, and
// an older import ID then returns 404. The one durable exception is the
// last-successful-import completion timestamp (migration 013, ADR-015): it is
// written by the shared importer inside migration.RunChunked and read by
// GET /api/corpus/import/metadata (metadata.go) directly from SQLite, so it
// survives restart independently of this manager's in-memory lifecycle.
//
// Uploads are staged by migration.Stager into a private application-owned
// directory (mode 0700, generated file names mode 0600); the client filename
// never influences a path and is never logged. The staged file is removed on
// every handled terminal path (completion, failure, cancellation, rejected
// acceptance) and abandoned staging files from an unclean exit are removed at
// manager construction. The import path logs neither raw titles, upload
// contents, client filenames, nor local staging paths (ARS-002 invariants
// 7-8).
//
// Cancellation (explicit or graceful shutdown) takes effect at a transaction
// boundary: the in-flight uncommitted chunk rolls back, previously committed
// chunks are preserved, and the terminal status reports the committed
// counters. Status is process-local; the HTTP layer maps it to the exact
// ARS-002 endpoint contract in handler.go.
package corpusimport

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/bateau84/YuHaul/internal/migration"
)

// Job status values (ARS-002 "status").
const (
	StatusQueued          = "queued"
	StatusRunning         = "running"
	StatusCancelRequested = "cancel_requested"
	StatusCancelled       = "cancelled"
	StatusCompleted       = "completed"
	StatusFailed          = "failed"
)

// MultipartOverheadBytes bounds the request body beyond the configured file
// cap: ARS-002 allows at most 1 MiB for multipart framing and headers on top
// of the file limit. The total request-body limit is
// maxUploadBytes + MultipartOverheadBytes.
const MultipartOverheadBytes int64 = 1 << 20

// Manager-level sentinel errors. The HTTP layer maps them to the ARS-002
// response contract; migration source rejections pass through unchanged and
// are classified by the handler.
var (
	// ErrImportActive is returned when an import is already active in this
	// process (HTTP: 409). No second import starts.
	ErrImportActive = errors.New("corpusimport: an import is already active")

	// ErrNotFound is returned when an import ID is unknown to this process,
	// including a job lost to a restart (HTTP: 404).
	ErrNotFound = errors.New("corpusimport: import not found")

	// ErrNoState is returned by Latest when the process holds no import state
	// at all (HTTP: 404).
	ErrNoState = errors.New("corpusimport: no import state available")
)

// Status is the JSON state shape of GET /api/corpus/import and
// GET /api/corpus/import/{import_id} (ARS-002). Every counter describes rows
// reconciled by committed destination transactions; inserted+skipped+errors
// always equals processed, and the reason counts always sum to errors.
type Status struct {
	ImportID                  string         `json:"import_id"`
	Status                    string         `json:"status"`
	Total                     int            `json:"total"`
	Processed                 int            `json:"processed"`
	Inserted                  int            `json:"inserted"`
	Skipped                   int            `json:"skipped"`
	Errors                    int            `json:"errors"`
	ErrorCountsByReason       map[string]int `json:"error_counts_by_reason"`
	RatePerSecond             *float64       `json:"rate_per_second"`
	EstimatedSecondsRemaining *int64         `json:"estimated_seconds_remaining"`
	TotalCorpusSize           *int64         `json:"total_corpus_size,omitempty"`

	// terminal marks a completed/cancelled/failed state, whose JSON always
	// carries total_corpus_size — the actual destination COUNT(*) when it can
	// be read, explicit null otherwise (ARS-002). Non-terminal states omit the
	// field.
	terminal bool
}

// MarshalJSON emits total_corpus_size on terminal states even when it is null,
// and omits it while an import is queued/running/cancel-requested.
func (s Status) MarshalJSON() ([]byte, error) {
	type live Status // same fields and tags, no methods
	if !s.terminal {
		return json.Marshal(live(s))
	}
	return json.Marshal(&struct {
		live
		TotalCorpusSize *int64 `json:"total_corpus_size"`
	}{live(s), s.TotalCorpusSize})
}

// IsTerminal reports whether the status is a terminal job state.
func (s Status) IsTerminal() bool { return isTerminal(s.Status) }

func isTerminal(status string) bool {
	switch status {
	case StatusCancelled, StatusCompleted, StatusFailed:
		return true
	default:
		return false
	}
}

// job is one in-process import operation. All mutable state (status, progress)
// is guarded by Manager.mu; ctx/cancel/done are fixed after acceptance.
type job struct {
	id       string
	staged   string // staged source path; owned by the job until termination
	schema   *migration.SourceSchema
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
	started  time.Time
	finished time.Time // terminal transition time; zero while active

	status   string
	progress migration.Progress
}

// Manager owns the process-local import state: at most one active job plus its
// most recent terminal result. A Manager is safe for concurrent use.
type Manager struct {
	dest           *sql.DB
	stager         *migration.Stager
	stagingDir     string
	maxUploadBytes int64
	logger         *slog.Logger
	chunkSize      int

	// runHook runs at the top of the worker goroutine before any importer
	// work; nil in production. Tests use it to hold a job in a known state.
	runHook func()

	// chunkHook runs after each committed chunk has been published, once the
	// manager lock is released; nil in production. Tests use it to hold an
	// import at a known committed boundary (the same technique the migration
	// package uses to cancel deterministically at a transaction boundary).
	chunkHook func(migration.Progress)

	mu     sync.Mutex
	latest *job // active job, or the most recent terminal one; nil after restart
}

// Option configures a Manager.
type Option func(*Manager)

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(m *Manager) {
		if l != nil {
			m.logger = l
		}
	}
}

// WithChunkSize sets the importer's rows-per-transaction size. It is an
// implementation tuning detail and not part of any public contract (ADR-009);
// a value <= 0 selects migration.DefaultChunkSize.
func WithChunkSize(n int) Option {
	return func(m *Manager) {
		if n > 0 {
			m.chunkSize = n
		}
	}
}

// NewManager creates the import manager, staging uploads under stagingDir
// (created mode 0700 if absent) and rejecting uploads larger than
// maxUploadBytes bytes. It removes abandoned staging files left by an unclean
// previous exit (ARS-002 invariant 7); startup cleanup handles only that crash
// recovery, never a live job.
func NewManager(dest *sql.DB, stagingDir string, maxUploadBytes int64, opts ...Option) (*Manager, error) {
	if dest == nil {
		return nil, errors.New("corpusimport: destination database is required")
	}
	if stagingDir == "" {
		return nil, errors.New("corpusimport: staging directory is required")
	}
	stager, err := migration.NewStager(stagingDir, maxUploadBytes)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		dest:           dest,
		stager:         stager,
		stagingDir:     stagingDir,
		maxUploadBytes: maxUploadBytes,
		logger:         slog.Default(),
		chunkSize:      migration.DefaultChunkSize,
	}
	for _, opt := range opts {
		opt(m)
	}
	m.cleanupAbandonedStaging()
	return m, nil
}

// StagingDir returns the private directory staged uploads live in.
func (m *Manager) StagingDir() string { return m.stagingDir }

// MaxUploadBytes returns the configured hard file-size cap.
func (m *Manager) MaxUploadBytes() int64 { return m.maxUploadBytes }

// cleanupAbandonedStaging removes crash leftovers from the dedicated staging
// directory. Only files with the server-generated staging name pattern are
// removed; anything else is left alone and merely logged, so a misconfigured
// directory can never lose unrelated operator data.
func (m *Manager) cleanupAbandonedStaging() {
	entries, err := os.ReadDir(m.stagingDir)
	if err != nil {
		m.logger.Warn("corpus import staging cleanup could not read the staging directory",
			"error", m.sanitize(err))
		return
	}
	removed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, migration.StagingFilePrefix) ||
			!strings.HasSuffix(name, migration.StagingFileSuffix) {
			m.logger.Warn("corpus import staging directory contains an unexpected entry", "entry", name)
			continue
		}
		if err := os.Remove(filepath.Join(m.stagingDir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			m.logger.Warn("corpus import staging cleanup could not remove an abandoned file",
				"error", m.sanitize(err))
			continue
		}
		removed++
	}
	if removed > 0 {
		m.logger.Info("removed abandoned corpus import staging files", "count", removed)
	}
}

// Latest returns the active import, or the most recent terminal one retained
// by this process. It returns ErrNoState when the process holds no import
// state (HTTP: 404, "no import state available").
func (m *Manager) Latest() (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.latest == nil {
		return Status{}, ErrNoState
	}
	return m.snapshotLocked(m.latest), nil
}

// Status returns the state of the identified import. It returns ErrNotFound
// for an unknown ID, including a job lost to a process restart (HTTP: 404,
// "import not found").
func (m *Manager) Status(id string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.latest == nil || m.latest.id != id {
		return Status{}, ErrNotFound
	}
	return m.snapshotLocked(m.latest), nil
}

// Cancel requests cancellation of the identified import. An unknown ID
// returns ErrNotFound. A terminal job keeps its unchanged terminal state; an
// active job transitions to cancel_requested (queued/running) or stays there,
// and the importer stops at the next transaction boundary. The HTTP layer
// answers 202 for a cancellation request and 200 for an already terminal job.
func (m *Manager) Cancel(id string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.latest == nil || m.latest.id != id {
		return Status{}, ErrNotFound
	}
	j := m.latest
	if !isTerminal(j.status) {
		if j.status == StatusQueued || j.status == StatusRunning {
			j.status = StatusCancelRequested
		}
		j.cancel()
	}
	return m.snapshotLocked(j), nil
}

// Shutdown requests cancellation of an active import, waits for its worker to
// reach the transaction boundary and remove the staged source, and returns
// nil. A context deadline returns an error instead of blocking forever; the
// caller decides whether to proceed with process teardown. Shutdown must be
// called before the destination database is closed (ARS-002 invariant 10).
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	j := m.latest
	var done chan struct{}
	if j != nil && !isTerminal(j.status) {
		if j.status == StatusQueued || j.status == StatusRunning {
			j.status = StatusCancelRequested
		}
		j.cancel()
		done = j.done
	}
	m.mu.Unlock()

	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("corpusimport: wait for the active import to stop: %w", ctx.Err())
	}
}

// startStaged accepts an already staged source file. On success the job owns
// stagedPath and removes it at termination; on failure startStaged removes it
// before returning, so no caller cleanup is needed.
func (m *Manager) startStaged(ctx context.Context, stagedPath string) (Status, error) {
	// Revalidate rather than trusting a prior preview (ARS-002): the source
	// must open read-only, pass schema discovery, and yield its row count
	// before any operation exists.
	schema, total, err := migration.Preflight(ctx, stagedPath)
	if err != nil {
		m.removeStaged(stagedPath)
		return Status{}, err
	}

	j := &job{
		id:     uuid.NewString(),
		staged: stagedPath,
		schema: schema,
		status: StatusQueued,
		progress: migration.Progress{
			Total:               int(total),
			ErrorCountsByReason: map[string]int{},
		},
	}
	j.ctx, j.cancel = context.WithCancel(context.Background())
	j.done = make(chan struct{})

	m.mu.Lock()
	if m.latest != nil && !isTerminal(m.latest.status) {
		// A concurrent start won the single active slot; the duplicate upload
		// is discarded (HTTP: 409).
		m.mu.Unlock()
		j.cancel()
		m.removeStaged(stagedPath)
		return Status{}, ErrImportActive
	}
	j.started = time.Now()
	m.latest = j
	m.mu.Unlock()

	m.logger.Info("corpus import accepted", "import_id", j.id, "total", int(total))
	go m.run(j)
	return m.statusOf(j), nil
}

// run executes one accepted import to a terminal state. It removes the staged
// source before signalling completion, so a Shutdown observer never returns
// while a raw upload is still on disk.
func (m *Manager) run(j *job) {
	// Registered first so it runs last: done closes only after the staged
	// source is removed and the source connection is closed.
	defer close(j.done)
	if m.runHook != nil {
		m.runHook()
	}

	m.mu.Lock()
	if j.status == StatusQueued {
		j.status = StatusRunning
	}
	m.mu.Unlock()
	defer m.removeStaged(j.staged)

	// Open the staged source read-only; the importer never writes to it.
	src, err := migration.OpenSourceReadOnly(context.Background(), j.staged)
	if err != nil {
		m.finish(j, migration.Progress{ErrorCountsByReason: map[string]int{}}, err)
		return
	}
	defer src.Close()

	importer := migration.NewImporterWithChunkSize(src, m.dest, j.id, m.chunkSize)
	chunkHook := m.chunkHook
	p, err := importer.RunChunked(j.ctx, j.schema, func(prog migration.Progress) {
		m.mu.Lock()
		j.progress = prog
		if j.ctx.Err() != nil && !isTerminal(j.status) {
			j.status = StatusCancelRequested
		}
		m.mu.Unlock()
		// Called after the lock is released so a test hook can block here
		// without deadlocking status readers.
		if chunkHook != nil {
			chunkHook(prog)
		}
	})
	m.finish(j, p, err)
}

// finish records the terminal state of a job. A cancellation observed by the
// context is terminal cancelled; any other importer error is terminal failed
// with its committed counters; a clean return is completed.
func (m *Manager) finish(j *job, p migration.Progress, runErr error) {
	if p.ErrorCountsByReason == nil {
		p.ErrorCountsByReason = map[string]int{}
	}
	if p.Total == 0 {
		// A cancellation or failure before the importer established its own
		// count must not erase the total this job's preflight already
		// established (ARS-002: total is the preflight source row count).
		p.Total = j.progress.Total
	}
	m.mu.Lock()
	j.progress = p
	switch {
	case runErr != nil && j.ctx.Err() != nil:
		j.status = StatusCancelled
	case runErr != nil:
		j.status = StatusFailed
	default:
		j.status = StatusCompleted
	}
	status := j.status
	j.finished = time.Now()
	m.mu.Unlock()

	if runErr != nil {
		m.logger.Warn("corpus import stopped with an error",
			"import_id", j.id, "status", status, "error", m.sanitize(runErr))
	}
	m.logger.Info("corpus import finished",
		"import_id", j.id,
		"status", status,
		"total", p.Total,
		"processed", p.Processed,
		"inserted", p.Inserted,
		"skipped", p.Skipped,
		"errors", p.Errors)
}

// statusOf snapshots one job's public state.
func (m *Manager) statusOf(j *job) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotLocked(j)
}

// snapshotLocked builds the wire state of a job. Callers hold m.mu.
func (m *Manager) snapshotLocked(j *job) Status {
	p := j.progress
	reasons := p.ErrorCountsByReason
	if reasons == nil {
		reasons = map[string]int{}
	}
	terminal := isTerminal(j.status)
	st := Status{
		ImportID:            j.id,
		Status:              j.status,
		Total:               p.Total,
		Processed:           p.Processed,
		Inserted:            p.Inserted,
		Skipped:             p.Skipped,
		Errors:              p.Errors,
		ErrorCountsByReason: reasons,
		TotalCorpusSize:     p.TotalCorpusSize,
		terminal:            terminal,
	}
	// Rate and ETA are approximate and may be null before a rate is available
	// (ARS-002). A terminal job's rate is frozen at its terminal transition so
	// it does not decay while the process idles; the ETA only exists while
	// work remains.
	end := j.finished
	if end.IsZero() {
		end = time.Now()
	}
	elapsed := end.Sub(j.started).Seconds()
	if p.Processed > 0 && elapsed > 0 {
		rate := float64(p.Processed) / elapsed
		st.RatePerSecond = &rate
		if !terminal && p.Total > p.Processed && rate > 0 {
			eta := int64(math.Round(float64(p.Total-p.Processed) / rate))
			st.EstimatedSecondsRemaining = &eta
		}
	}
	return st
}

// removeStaged deletes a staged upload, ignoring absence. Removal failures are
// logged without any staging path (ARS-002 invariant 8) and never fail the
// operation.
func (m *Manager) removeStaged(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.logger.Warn("corpus import could not remove a staged upload", "error", m.sanitize(err))
	}
}

// sanitize redacts local staging locations from an error message before it is
// logged, so the import path never logs a staging path or staged file name
// (ARS-002 invariant 8).
func (m *Manager) sanitize(err error) string {
	msg := err.Error()
	if m.stagingDir != "" {
		msg = strings.ReplaceAll(msg, m.stagingDir, "<staging>")
	}
	if i := strings.Index(msg, migration.StagingFilePrefix); i >= 0 {
		msg = msg[:i] + "<staged-upload>"
	}
	return msg
}
