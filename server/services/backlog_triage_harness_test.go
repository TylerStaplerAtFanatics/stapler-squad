//go:build harness

package services

// Headless triage test harness — no browser or UI required.
//
// Run all phases:
//
//	go test -v -tags=harness -run TestTriageHarness ./server/services/
//
// Run a specific phase:
//
//	go test -v -tags=harness -run TestTriageHarness/Gate         ./server/services/
//	go test -v -tags=harness -run TestTriageHarness/TriggerAndPoll ./server/services/
//	go test -v -tags=harness -run TestTriageHarness/ParserRobust ./server/services/
//	go test -v -tags=harness -run TestTriageHarness/FullFlow     ./server/services/

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sessionv1 "github.com/tstapler/stapler-squad/gen/proto/go/session/v1"
	"github.com/tstapler/stapler-squad/gen/proto/go/session/v1/sessionv1connect"
	"github.com/tstapler/stapler-squad/session/headless"
)

// setupTriageHarness spins up a real BacklogService + ConnectRPC handler
// behind an httptest.Server. Returns a typed client and the service itself.
func setupTriageHarness(t *testing.T, pool headless.PoolClient) (sessionv1connect.BacklogServiceClient, *BacklogService) {
	t.Helper()
	svc := NewBacklogService(createTestStorage(t), nil, nil, nil)
	svc.SetHeadlessPool(pool)
	t.Cleanup(svc.Shutdown)

	mux := http.NewServeMux()
	blPath, blHandler := sessionv1connect.NewBacklogServiceHandler(svc)
	mux.Handle(blPath, blHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := sessionv1connect.NewBacklogServiceClient(srv.Client(), srv.URL)
	return client, svc
}

// preambleTriageJSON wraps validTriageJSON() in natural-language preamble — the
// most common real-world LLM output pattern that broke the old parser.
func preambleTriageJSON() string {
	return "Triage complete. Here is my analysis:\n\n```json\n" + validTriageJSON() + "\n```"
}

// pollUntilReady polls GetBacklogItem until status == "ready" or timeout.
func pollUntilReady(t *testing.T, client sessionv1connect.BacklogServiceClient, itemID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		resp, err := client.GetBacklogItem(context.Background(),
			connect.NewRequest(&sessionv1.GetBacklogItemRequest{ItemId: itemID}))
		return err == nil && resp.Msg.Item.Status == "ready"
	}, 5*time.Second, 50*time.Millisecond, "item %s should reach 'ready' status within 5s", itemID)
}

// TestTriageHarness exercises the backlog triage feature end-to-end via the
// ConnectRPC HTTP layer. Each sub-test covers a distinct portion of the flow.
func TestTriageHarness(t *testing.T) {

	// ──────────────────────────────────────────────────────────────────────────
	// Gate: server must reject TriggerTriage when item has no repoPath.
	// This validates the backend precondition that mirrors the UI disabled state.
	// ──────────────────────────────────────────────────────────────────────────
	t.Run("Gate", func(t *testing.T) {
		client, _ := setupTriageHarness(t, &fakeHeadlessPool{response: validTriageJSON()})

		createResp, err := client.CreateBacklogItem(context.Background(),
			connect.NewRequest(&sessionv1.CreateBacklogItemRequest{
				Title:      "gate-test-item",
				Priority:   3,
				SkipTriage: true,
				// RepoPath intentionally empty
			}))
		require.NoError(t, err)
		itemID := createResp.Msg.Item.Id

		_, trigErr := client.TriggerTriage(context.Background(),
			connect.NewRequest(&sessionv1.TriggerTriageRequest{ItemId: itemID}))

		require.Error(t, trigErr, "TriggerTriage must fail when repoPath is empty")
		var connectErr *connect.Error
		require.ErrorAs(t, trigErr, &connectErr)
		assert.Equal(t, connect.CodeFailedPrecondition, connectErr.Code(),
			"expected FailedPrecondition, got: %v", connectErr.Code())
		t.Logf("Gate correctly blocked: %v", connectErr.Message())
	})

	// ──────────────────────────────────────────────────────────────────────────
	// TriggerAndPoll: happy path — trigger triage and poll until item is ready.
	// ──────────────────────────────────────────────────────────────────────────
	t.Run("TriggerAndPoll", func(t *testing.T) {
		pool := &fakeHeadlessPool{response: validTriageJSON()}
		client, _ := setupTriageHarness(t, pool)

		repoPath := t.TempDir()
		createResp, err := client.CreateBacklogItem(context.Background(),
			connect.NewRequest(&sessionv1.CreateBacklogItemRequest{
				Title:      "trigger-poll-item",
				Priority:   2,
				RepoPath:   repoPath,
				SkipTriage: true,
			}))
		require.NoError(t, err)
		itemID := createResp.Msg.Item.Id

		trigResp, trigErr := client.TriggerTriage(context.Background(),
			connect.NewRequest(&sessionv1.TriggerTriageRequest{ItemId: itemID}))
		require.NoError(t, trigErr)
		assert.Equal(t, "triage", trigResp.Msg.ItemSession.SessionRole)
		t.Logf("Triage triggered; item session ID: %s", trigResp.Msg.ItemSession.Id)

		pollUntilReady(t, client, itemID)

		getResp, getErr := client.GetBacklogItem(context.Background(),
			connect.NewRequest(&sessionv1.GetBacklogItemRequest{ItemId: itemID}))
		require.NoError(t, getErr)
		require.NotEmpty(t, getResp.Msg.Item.ItemSessions, "item must have at least one item session")

		is := getResp.Msg.Item.ItemSessions[0]
		assert.NotNil(t, is.EndedAt, "item session should be ended after triage completes")
		assert.Equal(t, "test summary", is.TriageResult.Summary)
		require.Len(t, is.TriageResult.Suggestions, 1)
		assert.Equal(t, "do X", is.TriageResult.Suggestions[0].Text)
		assert.Equal(t, 1, pool.callCount(), "headless pool should have been called exactly once")
		t.Logf("Triage result: summary=%q, tasks=%d", is.TriageResult.Summary, len(is.TriageResult.Tasks))
	})

	// ──────────────────────────────────────────────────────────────────────────
	// ParserRobust: pool returns preamble text before the JSON block — validates
	// the brace-scan fix in ParseHeadlessTriageResult.
	// ──────────────────────────────────────────────────────────────────────────
	t.Run("ParserRobust", func(t *testing.T) {
		pool := &fakeHeadlessPool{response: preambleTriageJSON()}
		client, _ := setupTriageHarness(t, pool)

		repoPath := t.TempDir()
		createResp, err := client.CreateBacklogItem(context.Background(),
			connect.NewRequest(&sessionv1.CreateBacklogItemRequest{
				Title:      "preamble-item",
				Priority:   3,
				RepoPath:   repoPath,
				SkipTriage: true,
			}))
		require.NoError(t, err)
		itemID := createResp.Msg.Item.Id

		_, trigErr := client.TriggerTriage(context.Background(),
			connect.NewRequest(&sessionv1.TriggerTriageRequest{ItemId: itemID}))
		require.NoError(t, trigErr)

		pollUntilReady(t, client, itemID)

		getResp, _ := client.GetBacklogItem(context.Background(),
			connect.NewRequest(&sessionv1.GetBacklogItemRequest{ItemId: itemID}))
		is := getResp.Msg.Item.ItemSessions[0]
		assert.Equal(t, "test summary", is.TriageResult.Summary,
			"parser must extract JSON even when LLM output has a preamble")
		assert.Equal(t, "ready", getResp.Msg.Item.Status,
			"item must reach 'ready' when preamble-wrapped JSON is parsed correctly")
		t.Logf("Parser robustness confirmed: summary=%q", is.TriageResult.Summary)
	})

	// ──────────────────────────────────────────────────────────────────────────
	// FullFlow: create without repoPath → gate blocks → set repoPath → triage
	// succeeds. Mirrors the exact user journey that was broken before the fix.
	// ──────────────────────────────────────────────────────────────────────────
	t.Run("FullFlow", func(t *testing.T) {
		pool := &fakeHeadlessPool{response: validTriageJSON()}
		client, _ := setupTriageHarness(t, pool)

		// Step 1: Create item without repoPath (via empty-state form path)
		createResp, err := client.CreateBacklogItem(context.Background(),
			connect.NewRequest(&sessionv1.CreateBacklogItemRequest{
				Title:      "full-flow-item",
				Priority:   1,
				SkipTriage: true,
			}))
		require.NoError(t, err)
		itemID := createResp.Msg.Item.Id
		t.Logf("[1/5] Created item %s (no repoPath)", itemID)

		// Step 2: Verify gate blocks triage
		_, gateErr := client.TriggerTriage(context.Background(),
			connect.NewRequest(&sessionv1.TriggerTriageRequest{ItemId: itemID}))
		require.Error(t, gateErr, "[2/5] Gate must block triage when repoPath is empty")
		t.Log("[2/5] Gate correctly blocked triage (no repoPath)")

		// Step 3: Update item with repoPath (user fills in the field)
		repoPath := t.TempDir()
		_, updateErr := client.UpdateBacklogItem(context.Background(),
			connect.NewRequest(&sessionv1.UpdateBacklogItemRequest{
				ItemId:   itemID,
				RepoPath: repoPath,
			}))
		require.NoError(t, updateErr)
		t.Logf("[3/5] Set repoPath to %s", repoPath)

		// Step 4: Trigger triage — should now succeed
		trigResp, trigErr := client.TriggerTriage(context.Background(),
			connect.NewRequest(&sessionv1.TriggerTriageRequest{ItemId: itemID}))
		require.NoError(t, trigErr, "[4/5] TriggerTriage must succeed after repoPath is set")
		t.Logf("[4/5] Triage triggered; session role=%s", trigResp.Msg.ItemSession.SessionRole)

		// Step 5: Poll and verify completion
		pollUntilReady(t, client, itemID)
		getResp, _ := client.GetBacklogItem(context.Background(),
			connect.NewRequest(&sessionv1.GetBacklogItemRequest{ItemId: itemID}))
		assert.Equal(t, "ready", getResp.Msg.Item.Status)
		assert.Equal(t, "test summary", getResp.Msg.Item.ItemSessions[0].TriageResult.Summary)
		t.Log("[5/5] Full triage flow completed — item is ready with triage result")
	})
}
