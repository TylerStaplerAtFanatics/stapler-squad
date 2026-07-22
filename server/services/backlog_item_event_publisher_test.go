package services

import (
	"context"
	"testing"
	"time"

	"github.com/tstapler/stapler-squad/server/events"
	"github.com/tstapler/stapler-squad/session"
)

// TestBacklogItemEventPublisher_should_publishConvertedEventToBus_When_PublishItemChangedCalled
// verifies PublishItemChanged converts a session.BacklogItemChange into an
// events.BacklogItemEventPayload and delivers it through a real EventBus
// (Story 1.3.2 AC).
func TestBacklogItemEventPublisher_should_publishConvertedEventToBus_When_PublishItemChangedCalled(t *testing.T) {
	bus := events.NewEventBus(10)
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, _ := bus.Subscribe(ctx)

	publisher := &BacklogItemEventPublisher{Bus: bus}
	item := &session.BacklogItemData{ID: "item-123", Title: "test item"}

	publisher.PublishItemChanged(item, session.BacklogItemChange{
		Kind:      session.ChangeStatusTransition,
		OldStatus: "review",
		NewStatus: "done",
	})

	select {
	case received := <-eventCh:
		if received.Type != events.EventBacklogItemChanged {
			t.Fatalf("expected event type %s, got %s", events.EventBacklogItemChanged, received.Type)
		}
		if received.BacklogItemPayload == nil {
			t.Fatal("expected BacklogItemPayload to be non-nil")
		}
		if received.BacklogItemPayload.NewStatus != "done" {
			t.Errorf("expected NewStatus 'done', got %q", received.BacklogItemPayload.NewStatus)
		}
		if received.BacklogItemPayload.OldStatus != "review" {
			t.Errorf("expected OldStatus 'review', got %q", received.BacklogItemPayload.OldStatus)
		}
		if received.BacklogItemPayload.Kind != events.BacklogChangeStatusTransition {
			t.Errorf("expected Kind %s, got %s", events.BacklogChangeStatusTransition, received.BacklogItemPayload.Kind)
		}
		if received.BacklogItemPayload.Item != item {
			t.Error("expected payload Item to be the same pointer passed in")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

// panickingChangeKind is a BacklogChangeKind value with no mapping in
// mapBacklogChangeKind, used to force a panic inside PublishItemChanged's
// payload-construction step without needing a separate test double type.
const panickingChangeKind session.BacklogChangeKind = "not-a-real-kind"

// TestBacklogItemEventPublisher_should_recoverAndLog_When_PublishItemChangedPanics
// verifies that a panic during payload construction (an unmapped
// BacklogChangeKind) is recovered inside PublishItemChanged and never
// propagates to the caller (Story 1.3.2 AC / Task 1.3.2b).
func TestBacklogItemEventPublisher_should_recoverAndLog_When_PublishItemChangedPanics(t *testing.T) {
	bus := events.NewEventBus(10)
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventCh, _ := bus.Subscribe(ctx)

	publisher := &BacklogItemEventPublisher{Bus: bus}
	item := &session.BacklogItemData{ID: "item-456"}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("PublishItemChanged panic reached the caller (recover() failed to contain it): %v", r)
			}
		}()
		publisher.PublishItemChanged(item, session.BacklogItemChange{Kind: panickingChangeKind})
	}()

	// No event should have been published — the panic happened before
	// bus.Publish was ever called.
	select {
	case received := <-eventCh:
		t.Fatalf("expected no event to be published after a panicking construction, got %+v", received)
	case <-time.After(100 * time.Millisecond):
		// Expected: nothing arrives.
	}
}

// TestBacklogItemEventPublisher_should_noOp_When_BusIsNil verifies the nil-Bus
// guard: a zero-value publisher (or one constructed without a Bus) must not
// panic when PublishItemChanged is called.
func TestBacklogItemEventPublisher_should_noOp_When_BusIsNil(t *testing.T) {
	publisher := &BacklogItemEventPublisher{}
	item := &session.BacklogItemData{ID: "item-789"}

	publisher.PublishItemChanged(item, session.BacklogItemChange{Kind: session.ChangeStatusTransition})
	// No assertion beyond "did not panic" — reaching this line is the test.
}
