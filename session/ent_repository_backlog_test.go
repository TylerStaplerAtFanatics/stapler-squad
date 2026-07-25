package session

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetBacklogItem_ExternalURL_ReadsEmptyStringForPreExistingRow proves AC2's
// NULL-safety requirement: a row created before external_url existed (so the
// column scans from SQL NULL) reads back ExternalURL == "" with no panic.
func TestGetBacklogItem_ExternalURL_ReadsEmptyStringForPreExistingRow(t *testing.T) {
	repo, cleanup := createTestEntRepository(t)
	defer cleanup()

	ctx := context.Background()

	// Create directly via the ent client without setting ExternalURL, simulating
	// a row written before this column existed.
	item, err := repo.client.BacklogItem.Create().
		SetTitle("pre-existing item").
		Save(ctx)
	require.NoError(t, err)

	fetched, err := repo.client.BacklogItem.Get(ctx, item.ID)
	require.NoError(t, err)
	assert.Equal(t, "", fetched.ExternalURL)
}
