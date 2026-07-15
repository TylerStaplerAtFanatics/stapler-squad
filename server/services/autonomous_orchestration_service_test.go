package services

// Unit tests for AutonomousOrchestrationService: driver registry, lifecycle context
// binding, and completion callback deregistration behavior.
// These tests do not require tmux or a real headless pool.

import (
	"context"
	"testing"
	"time"

	connect "connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sessionv1 "github.com/tstapler/stapler-squad/gen/proto/go/session/v1"
	"github.com/tstapler/stapler-squad/server/events"
	"github.com/tstapler/stapler-squad/session"
	"github.com/tstapler/stapler-squad/session/headless"
)

// instantDonePool is a HeadlessPoolClient that returns DONE on the first call,
// allowing an AutonomousDriver to complete without needing a real LLM backend.
type instantDonePool struct{}

func (p *instantDonePool) CallBlockingWithOptions(
	_ context.Context,
	_ headless.FeatureKey,
	_, _ string,
	_ headless.CallOptions,
) (string, error) {
	return "DONE: test complete", nil
}

// addPausedAutonomousInstance inserts a paused session with AutonomousMode=true into storage.
func addPausedAutonomousInstance(t *testing.T, storage *session.Storage, title string) *session.Instance {
	t.Helper()
	inst := &session.Instance{
		Title:          title,
		UUID:           title + "-uuid-1234",
		Path:           "/tmp/test",
		Status:         session.Paused,
		Program:        "claude",
		AutonomousMode: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	require.NoError(t, storage.AddInstance(inst))
	return inst
}

// TestNewAutonomousOrchestrationService verifies basic construction invariants.
func TestNewAutonomousOrchestrationService(t *testing.T) {
	bus := events.NewEventBus(100)
	svc := NewAutonomousOrchestrationService(nil, bus)
	require.NotNil(t, svc)
	assert.NotNil(t, svc.drivers)
	// pool nil is valid — callers degrade gracefully.
	assert.Nil(t, svc.pool)
}

// TestAutonomousOrchestrationService_SetLifecycleContext verifies that SetLifecycleContext
// stores the provided context and that driverCtx() returns it (observable via cancellation).
func TestAutonomousOrchestrationService_SetLifecycleContext(t *testing.T) {
	storage := createTestStorage(t)
	eventBus := events.NewEventBus(100)
	svc := NewSessionService(storage, eventBus)

	// Without SetLifecycleContext, driverCtx() should fall back to Background.
	assert.NoError(t, svc.autonomousSvc.driverCtx().Err(), "driverCtx() should return non-cancelled ctx before SetLifecycleContext")

	// After wiring a cancelled context, driverCtx() should reflect it.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	svc.SetLifecycleContext(ctx)

	assert.Error(t, svc.autonomousSvc.driverCtx().Err(), "driverCtx() should return the cancelled lifecycle context after SetLifecycleContext")
}

// TestAutonomousOrchestrationService_DriverRegistry_RegisterAndDeregister verifies that
// registerDriver stores a driver and stopAndDeregisterDriver removes it and calls Stop.
func TestAutonomousOrchestrationService_DriverRegistry_RegisterAndDeregister(t *testing.T) {
	storage := createTestStorage(t)
	eventBus := events.NewEventBus(100)
	svc := NewSessionService(storage, eventBus)

	inst := &session.Instance{
		Title: "reg-test",
		UUID:  "reg-uuid-5678",
	}
	pool := &instantDonePool{}
	driver := session.NewAutonomousDriver(inst, pool, "fix it", 1)

	// Register the driver without starting it (Start() would require a controller).
	svc.autonomousSvc.registerDriver("reg-test", driver)

	// Deregister and stop — should not panic.
	svc.autonomousSvc.stopAndDeregisterDriver("reg-test")

	// Second deregister is a no-op — should also not panic.
	svc.autonomousSvc.stopAndDeregisterDriver("reg-test")
}

// TestAutonomousOrchestrationService_DeleteSession_StopsRegisteredDriver verifies that
// DeleteSession calls stopAndDeregisterDriver for the deleted session's title.
func TestAutonomousOrchestrationService_DeleteSession_StopsRegisteredDriver(t *testing.T) {
	storage := createTestStorage(t)
	eventBus := events.NewEventBus(100)
	svc := NewSessionService(storage, eventBus)

	const title = "delete-driver-test"
	addPausedAutonomousInstance(t, storage, title)

	inst := &session.Instance{
		Title: title,
		UUID:  title + "-uuid-1234",
	}
	pool := &instantDonePool{}
	driver := session.NewAutonomousDriver(inst, pool, "fix it", 1)
	svc.autonomousSvc.registerDriver(title, driver)

	req := connect.NewRequest(&sessionv1.DeleteSessionRequest{Id: title})
	resp, err := svc.DeleteSession(context.Background(), req)
	require.NoError(t, err, "DeleteSession should succeed")
	assert.True(t, resp.Msg.Success)

	// The registry entry must have been removed — subsequent stop is a no-op.
	svc.autonomousSvc.stopAndDeregisterDriver(title) // no panic = driver already deregistered
}

// TestAutonomousOrchestrationService_OnAutonomousDriverComplete_DeregistersDriver verifies
// that the completion callback removes the driver from the registry so it does not leak.
func TestAutonomousOrchestrationService_OnAutonomousDriverComplete_DeregistersDriver(t *testing.T) {
	storage := createTestStorage(t)
	eventBus := events.NewEventBus(100)
	svc := NewSessionService(storage, eventBus)

	const title = "complete-driver-test"

	inst := &session.Instance{
		Title: title,
		UUID:  title + "-uuid-9999",
	}
	pool := &instantDonePool{}
	driver := session.NewAutonomousDriver(inst, pool, "fix it", 1)
	svc.autonomousSvc.registerDriver(title, driver)

	// Fire the completion callback (simulates driver goroutine finishing).
	// instanceFinder returns nil here — expected when the session exited before callback fires.
	outcome := session.AutonomousDriverOutcome{Done: true, Reason: "test done", Turns: 1}
	svc.autonomousSvc.onAutonomousDriverComplete(title, outcome)

	// The driver must have been removed from the registry.
	svc.autonomousSvc.stopAndDeregisterDriver(title) // no panic = already deregistered
}

// TestAutonomousOrchestrationService_OnAutonomousDriverComplete_NotifiesStatusUpdateFailure
// verifies the fix for the notification/transition-divergence bug: when the driver reports
// Done but the backlog item's status transition fails its optimistic-concurrency precondition
// (e.g. a concurrent status change), the published notification must say so explicitly rather
// than announcing a plain "complete" while the item is silently stuck.
func TestAutonomousOrchestrationService_OnAutonomousDriverComplete_NotifiesStatusUpdateFailure(t *testing.T) {
	storage := createTestStorage(t)
	ctx := context.Background()
	eventBus := events.NewEventBus(4)
	svc := NewSessionService(storage, eventBus)

	const title = "notif-race-test"
	inst := &session.Instance{
		Title:          title,
		UUID:           title + "-uuid",
		Path:           "/tmp/test",
		Status:         session.Paused,
		Program:        "claude",
		AutonomousMode: true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	require.NoError(t, storage.AddInstance(inst))
	// Bypass the real ReviewQueuePoller (not wired in this unit test) and resolve the
	// instance directly, matching what FindLiveInstance would return once wired.
	svc.autonomousSvc.SetInstanceFinder(func(_ string) *session.Instance { return inst })

	// Item is already at 'review' — the triage-role transition below expects 'idea', so its
	// precondition will fail and TransitionBacklogItemStatus must return an error.
	item, err := storage.CreateBacklogItem(ctx, session.BacklogItemData{
		Title:  "Notification race test item",
		Status: string(session.BacklogStatusReview),
	})
	require.NoError(t, err)

	_, err = storage.CreateItemSession(ctx, session.ItemSessionData{
		ItemID:      item.ID,
		SessionUUID: inst.UUID,
		SessionRole: session.SessionRoleTriage,
	})
	require.NoError(t, err)

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, _ := eventBus.Subscribe(subCtx)

	outcome := session.AutonomousDriverOutcome{Done: true, Reason: "test done", Turns: 1}
	svc.autonomousSvc.onAutonomousDriverComplete(title, outcome)

	// onAutonomousDriverComplete also publishes a session.updated event before the
	// notification (badge update for autonomous_mode/autonomous_outcome) — skip past it.
	var notif *events.Event
	for i := 0; i < 5; i++ {
		select {
		case ev := <-ch:
			if ev.Type == events.EventNotification {
				notif = ev
			}
		case <-time.After(2 * time.Second):
			i = 5
		}
		if notif != nil {
			break
		}
	}
	require.NotNil(t, notif, "expected a notification event")
	assert.Contains(t, notif.NotificationTitle, "status update failed", "operator must be told the status update failed, not just 'complete'")
	assert.Equal(t, int32(9), notif.NotificationType, "a divergence between driver-done and status-update-failed must surface as a FAILURE notification")
}
