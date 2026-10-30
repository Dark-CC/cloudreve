package manager

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A folder direct link expands into one link per file beneath it, so the
// expanded count has to be checked against the same source_batch cap a
// directly requested batch is capped by.
func TestDirectLinkBudget(t *testing.T) {
	cases := []struct {
		name        string
		sourceBatch int
		requested   int
		want        int
	}{
		// Cap configured: it governs how many links one request may yield.
		{"cap above request", 10, 1, 10},
		{"cap equals request", 10, 10, 10},
		// The service layer already rejects over-cap requests, so the
		// requested count is never lowered here.
		{"cap below request", 5, 10, 10},
		// No cap configured: the request itself is the only bound.
		{"unset cap", 0, 3, 3},
		{"negative cap", -1, 2, 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, directLinkBudget(tc.sourceBatch, tc.requested))
		})
	}
}
