package services

// backlog_service_events_test.go — proto shape tests for BacklogItemEvent,
// covering Story 1.1.1's acceptance criteria (project_plans/
// backlog-event-driven-updates/implementation/plan.md, validation.md R1).
//
// These test the generated oneof accessors directly; there is no handler to
// exercise yet (that's Epic 3.1 / Story 3.1.1, out of scope here).

import (
	"testing"

	sessionv1 "github.com/tstapler/stapler-squad/gen/proto/go/session/v1"
)

func TestBacklogItemEvent_should_exposeCorrectOneofVariant_When_StatusChangedIsSet(t *testing.T) {
	event := &sessionv1.BacklogItemEvent{
		Event: &sessionv1.BacklogItemEvent_StatusChanged{
			StatusChanged: &sessionv1.BacklogItemStatusChangedEvent{
				ItemId:    "abc123",
				OldStatus: "in_progress",
				NewStatus: "review",
			},
		},
	}

	if got := event.GetStatusChanged().GetNewStatus(); got != "review" {
		t.Errorf("GetStatusChanged().GetNewStatus() = %q, want %q", got, "review")
	}
	if got := event.GetVerdictRecorded(); got != nil {
		t.Errorf("GetVerdictRecorded() = %v, want nil", got)
	}
}

func TestBacklogItemEvent_should_returnNilForUnsetOneofVariant_When_NoVariantIsSet(t *testing.T) {
	event := &sessionv1.BacklogItemEvent{}

	if got := event.GetStatusChanged(); got != nil {
		t.Errorf("GetStatusChanged() = %v, want nil", got)
	}
	if got := event.GetVerdictRecorded(); got != nil {
		t.Errorf("GetVerdictRecorded() = %v, want nil", got)
	}
	if got := event.GetSessionAttached(); got != nil {
		t.Errorf("GetSessionAttached() = %v, want nil", got)
	}
	if got := event.GetItemUpdated(); got != nil {
		t.Errorf("GetItemUpdated() = %v, want nil", got)
	}
	if got := event.GetItemArchived(); got != nil {
		t.Errorf("GetItemArchived() = %v, want nil", got)
	}
	if got := event.GetItemRemoved(); got != nil {
		t.Errorf("GetItemRemoved() = %v, want nil", got)
	}
}
