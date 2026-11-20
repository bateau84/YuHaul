package pipeline

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bateau84/YuHaul/internal/diag"
)

// A guard-blocked video keeps the registered uncertain-mutation code in every
// stored reason path, never unknown_error.
func TestGuardBlockedReasonCarriesRegisteredCode(t *testing.T) {
	want := "diag:v1 phase=action_execution operation=execute_action code=mutation_application_uncertain"
	for name, err := range map[string]error{
		"executor failure": &videoActionFailure{cause: errGuardBlocked()},
		"guarded retry":    guardedRetryHandoffError{cause: errGuardBlocked()},
	} {
		require.Equal(t, want, diag.Reason(err, "retry failed"), name)
		require.Equal(t, want, diag.Encode(err, "action_execution", "execute_action"), name)
		require.True(t, diag.Safe(want))
	}
}
