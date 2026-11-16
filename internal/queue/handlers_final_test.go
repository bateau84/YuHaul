package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// K2 (HTTP): both retry routes answer 409 video_already_final with the final
// outcome and the closed entry; a second retry gives the same answer.
func TestRetryRoutesAnswerAlreadyFinal409K2(t *testing.T) {
	s := newTestStore(t)
	origin := retryRun(t, s.db)
	video := seedVideo(t, s.db, origin, "http-final")
	exec(t, s.db, `UPDATE video SET final_outcome='rejected', processing_state='completed' WHERE id=?`, video)
	errID := exec(t, s.db, `INSERT INTO error_entry(video_id,run_id,error_category,error_message,status) VALUES(?,?,'runtime','m','active')`, video, origin)
	item := deferredSibling(t, s, video, origin, DeferredStatusPending)

	svc := NewService(s, nil).WithRetryCoordinator(finalCoordinator{s})
	for _, tc := range []struct {
		name    string
		handler http.Handler
		id      int64
	}{{"error", svc.ErrorHandler(), errID}, {"deferred", svc.DeferredHandler(), item}} {
		t.Run(tc.name, func(t *testing.T) {
			var first string
			for round := range 2 {
				rec := httptest.NewRecorder()
				tc.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/"+itoa(tc.id)+"/retry", nil))
				require.Equal(t, http.StatusConflict, rec.Code, "round %d", round)
				var body struct {
					Code         string         `json:"code"`
					FinalOutcome string         `json:"final_outcome"`
					Error        string         `json:"error"`
					Entry        map[string]any `json:"entry"`
				}
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
				require.Equal(t, "video_already_final", body.Code)
				require.Equal(t, "rejected", body.FinalOutcome)
				require.NotEmpty(t, body.Error)
				require.Equal(t, float64(tc.id), body.Entry["id"])
				require.Equal(t, "final_outcome", body.Entry["closed_reason"])
				require.Equal(t, "rejected", body.Entry["video_final_outcome"])
				if round == 0 {
					first = rec.Body.String()
				} else {
					require.JSONEq(t, first, rec.Body.String(), "the second retry answers the same 409")
				}
			}
		})
	}
}

// finalCoordinator mirrors the pipeline's retry refusal: one transaction, no
// run, the closed family committed, then the typed error.
type finalCoordinator struct{ s *Store }

func (c finalCoordinator) RetryQueueEntry(ctx context.Context, id int64, errorQueue bool) error {
	var refusal *AlreadyFinalError
	err := c.s.WithRetryTx(ctx, func(tx *sql.Tx) error {
		var err error
		refusal, _, err = c.s.RefuseFinalRetryTx(ctx, tx, id, errorQueue, time.Now())
		return err
	})
	if err != nil {
		return err
	}
	if refusal != nil {
		return refusal
	}
	return nil
}
