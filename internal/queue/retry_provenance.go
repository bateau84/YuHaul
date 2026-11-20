package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ErrRetryProvenance marks a retry claim whose durable provenance
// (run_video_outcome.retry_claim_provenance, run-initiation ARS-005 §5.1.1) is
// missing, malformed, unsupported or different from the receipt a caller holds.
// It is an integrity failure: distinct from proved removal (ErrWorkRemoved),
// lost ownership (ErrWorkNotOwned) and storage I/O errors. Nothing may be
// dispatched, reset, handed off or have a guard changed for such work.
var ErrRetryProvenance = errors.New("queue: retry claim provenance is missing or invalid")

const retryProvenanceVersion = 1

type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// encodeRetryProvenance renders the canonical version 1 value for the exact
// claimed rows of receipt. Both arrays are always present.
func encodeRetryProvenance(receipt RetryGroupReceipt) (string, error) {
	if err := validateProvenanceRefs(receipt.Deferred, receipt.Errors); err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, `{"version":%d,"deferred_rows":`, retryProvenanceVersion)
	writeProvenanceRefs(&b, receipt.Deferred)
	b.WriteString(`,"error_rows":`)
	writeProvenanceRefs(&b, receipt.Errors)
	b.WriteString(`}`)
	return b.String(), nil
}

func writeProvenanceRefs(b *strings.Builder, refs []RetryRowRef) {
	b.WriteByte('[')
	for i, ref := range refs {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(b, `{"id":%d,"origin_run_id":%d}`, ref.ID, ref.OriginRunID)
	}
	b.WriteByte(']')
}

// validateProvenanceRefs enforces the canonical member rules: positive IDs,
// strictly increasing within each queue kind, and at least one row.
func validateProvenanceRefs(deferred, errs []RetryRowRef) error {
	if len(deferred)+len(errs) == 0 {
		return errors.New("no claimed rows")
	}
	for _, group := range []struct {
		name string
		refs []RetryRowRef
	}{{"deferred_rows", deferred}, {"error_rows", errs}} {
		name := group.name
		var last int64
		for _, ref := range group.refs {
			if ref.ID <= 0 || ref.OriginRunID <= 0 {
				return fmt.Errorf("%s: non-positive id %d or origin_run_id %d", name, ref.ID, ref.OriginRunID)
			}
			if ref.ID <= last {
				return fmt.Errorf("%s: id %d is not strictly after %d", name, ref.ID, last)
			}
			last = ref.ID
		}
	}
	return nil
}

// decodeRetryProvenance parses a stored value strictly: exactly the three
// version 1 fields, no duplicate or unknown fields at any level, integer
// numbers only, canonical member sets. Whitespace and key order are free.
func decodeRetryProvenance(raw string) (deferred, errs []RetryRowRef, err error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	if err := expectDelim(dec, '{'); err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	for dec.More() {
		key, err := readKey(dec)
		if err != nil {
			return nil, nil, err
		}
		if seen[key] {
			return nil, nil, fmt.Errorf("duplicate field %q", key)
		}
		seen[key] = true
		switch key {
		case "version":
			n, err := readInt(dec)
			if err != nil {
				return nil, nil, fmt.Errorf("version: %w", err)
			}
			if n != retryProvenanceVersion {
				return nil, nil, fmt.Errorf("unsupported version %d", n)
			}
		case "deferred_rows":
			if deferred, err = readRefs(dec); err != nil {
				return nil, nil, fmt.Errorf("deferred_rows: %w", err)
			}
		case "error_rows":
			if errs, err = readRefs(dec); err != nil {
				return nil, nil, fmt.Errorf("error_rows: %w", err)
			}
		default:
			return nil, nil, fmt.Errorf("unknown field %q", key)
		}
	}
	if err := expectDelim(dec, '}'); err != nil {
		return nil, nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, nil, errors.New("trailing data after object")
	}
	if len(seen) != 3 {
		return nil, nil, errors.New("version, deferred_rows and error_rows are all required")
	}
	if err := validateProvenanceRefs(deferred, errs); err != nil {
		return nil, nil, err
	}
	return deferred, errs, nil
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != want {
		return fmt.Errorf("expected %q, found %v", want, tok)
	}
	return nil
}

func readKey(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	key, ok := tok.(string)
	if !ok {
		return "", fmt.Errorf("expected field name, found %v", tok)
	}
	return key, nil
}

func readInt(dec *json.Decoder) (int64, error) {
	tok, err := dec.Token()
	if err != nil {
		return 0, err
	}
	num, ok := tok.(json.Number)
	if !ok {
		return 0, fmt.Errorf("expected an integer, found %v", tok)
	}
	n, err := strconv.ParseInt(num.String(), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("expected a signed 64-bit integer, found %s", num)
	}
	return n, nil
}

func readRefs(dec *json.Decoder) ([]RetryRowRef, error) {
	if err := expectDelim(dec, '['); err != nil {
		return nil, err
	}
	var refs []RetryRowRef // nil when empty, as the claim builds it
	for dec.More() {
		if err := expectDelim(dec, '{'); err != nil {
			return nil, err
		}
		var ref RetryRowRef
		seen := map[string]bool{}
		for dec.More() {
			key, err := readKey(dec)
			if err != nil {
				return nil, err
			}
			if seen[key] {
				return nil, fmt.Errorf("duplicate field %q", key)
			}
			seen[key] = true
			var n int64
			switch key {
			case "id", "origin_run_id":
				if n, err = readInt(dec); err != nil {
					return nil, fmt.Errorf("%s: %w", key, err)
				}
			default:
				return nil, fmt.Errorf("unknown member field %q", key)
			}
			if key == "id" {
				ref.ID = n
			} else {
				ref.OriginRunID = n
			}
		}
		if err := expectDelim(dec, '}'); err != nil {
			return nil, err
		}
		if len(seen) != 2 {
			return nil, errors.New("each member needs exactly id and origin_run_id")
		}
		refs = append(refs, ref)
	}
	if err := expectDelim(dec, ']'); err != nil {
		return nil, err
	}
	return refs, nil
}

// storedRetryReceipt reads the claim identity the receiving run stored for
// videoID. present is false when there is no membership row or its value is
// NULL. A present but undecodable value returns ErrRetryProvenance. It never
// consults owner pointers.
func storedRetryReceipt(ctx context.Context, q rowQuerier, runID, videoID int64) (receipt RetryGroupReceipt, present bool, err error) {
	var raw sql.NullString
	err = q.QueryRowContext(ctx, `SELECT retry_claim_provenance FROM run_video_outcome WHERE run_id=? AND video_id=?`, runID, videoID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return RetryGroupReceipt{}, false, nil
	}
	if err != nil {
		return RetryGroupReceipt{}, false, fmt.Errorf("read retry claim provenance of run %d video %d: %w", runID, videoID, err)
	}
	if !raw.Valid {
		return RetryGroupReceipt{}, false, nil
	}
	deferred, errs, err := decodeRetryProvenance(raw.String)
	if err != nil {
		return RetryGroupReceipt{}, false, fmt.Errorf("run %d video %d: %v: %w", runID, videoID, err, ErrRetryProvenance)
	}
	return RetryGroupReceipt{ReceivingRunID: runID, VideoID: videoID, Deferred: deferred, Errors: errs}, true, nil
}

// requireStoredRetryReceipt is storedRetryReceipt for work that needs exact
// retry authority: absence is an integrity failure.
func requireStoredRetryReceipt(ctx context.Context, q rowQuerier, runID, videoID int64) (RetryGroupReceipt, error) {
	receipt, present, err := storedRetryReceipt(ctx, q, runID, videoID)
	if err != nil {
		return RetryGroupReceipt{}, err
	}
	if !present {
		return RetryGroupReceipt{}, fmt.Errorf("run %d video %d has no stored claim provenance: %w", runID, videoID, ErrRetryProvenance)
	}
	return receipt, nil
}

// sameRetryRefs compares decoded identities, not encodings.
func sameRetryRefs(a, b []RetryRowRef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameRetryReceipt(a, b RetryGroupReceipt) bool {
	return a.ReceivingRunID == b.ReceivingRunID && a.VideoID == b.VideoID &&
		sameRetryRefs(a.Deferred, b.Deferred) && sameRetryRefs(a.Errors, b.Errors)
}

// claimRowRefs lists the exact rows a fresh claim takes, by ID and origin.
func claimRowRefs(ctx context.Context, tx *sql.Tx, table string, videoID int64, statuses string) ([]RetryRowRef, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, run_id FROM `+table+` WHERE video_id=? AND status IN (`+statuses+`) ORDER BY id`, videoID)
	if err != nil {
		return nil, fmt.Errorf("list %s rows to claim: %w", table, err)
	}
	defer rows.Close()
	var refs []RetryRowRef
	for rows.Next() {
		var ref RetryRowRef
		if err := rows.Scan(&ref.ID, &ref.OriginRunID); err != nil {
			return nil, fmt.Errorf("scan %s row to claim: %w", table, err)
		}
		refs = append(refs, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s rows to claim: %w", table, err)
	}
	return refs, nil
}
