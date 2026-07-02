package services

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sessionv1 "github.com/tstapler/stapler-squad/gen/proto/go/session/v1"
	"github.com/tstapler/stapler-squad/server/events"
	"github.com/tstapler/stapler-squad/session"
	"github.com/tstapler/stapler-squad/session/detection"
)

// TestConvertEventToProto_SessionUpdated_WithDetection verifies that an event created
// via NewSessionUpdatedEventWithDetection carries its typed DetectedStatus and
// DetectedContext through to the outgoing SessionUpdatedEvent proto. This is the branch
// in convertEventToProto guarded by `event.DetectedStatusTyped != detection.StatusUnknown`;
// NewSessionUpdatedEventWithDetection has exactly one production call site (the
// UpdateSession RPC path) and, before this test, zero coverage anywhere in the repo.
func TestConvertEventToProto_SessionUpdated_WithDetection(t *testing.T) {
	inst := &session.Instance{Title: "detected-session"}
	evt := events.NewSessionUpdatedEventWithDetection(
		inst,
		[]string{"status"},
		detection.StatusNeedsApproval,
		"waiting for tool approval",
	)

	proto := convertEventToProto(evt)
	require.NotNil(t, proto)

	updated := proto.GetSessionUpdated()
	require.NotNil(t, updated, "expected a SessionUpdated event payload")

	assert.Equal(t, sessionv1.DetectedStatus_DETECTED_STATUS_NEEDS_APPROVAL, updated.DetectedStatus,
		"DetectedStatus should reflect the typed detection status passed to the event")
	assert.Equal(t, "waiting for tool approval", updated.DetectedContext,
		"DetectedContext should be carried through to the wire")
}

// TestConvertEventToProto_SessionUpdated_NoDetection verifies that a plain
// NewSessionUpdatedEvent (no detection info attached) results in
// DETECTED_STATUS_UNSPECIFIED and an empty DetectedContext on the wire. Without this
// guard, the zero value of DetectedStatusTyped (detection.StatusUnknown) could leak
// through DetectedStatusToProto as DETECTED_STATUS_UNKNOWN instead of UNSPECIFIED.
func TestConvertEventToProto_SessionUpdated_NoDetection(t *testing.T) {
	inst := &session.Instance{Title: "plain-session"}
	evt := events.NewSessionUpdatedEvent(inst, []string{"status"})

	proto := convertEventToProto(evt)
	require.NotNil(t, proto)

	updated := proto.GetSessionUpdated()
	require.NotNil(t, updated, "expected a SessionUpdated event payload")

	assert.Equal(t, sessionv1.DetectedStatus_DETECTED_STATUS_UNSPECIFIED, updated.DetectedStatus,
		"DetectedStatus should remain unset when no detection info is present")
	assert.Empty(t, updated.DetectedContext,
		"DetectedContext should remain empty when no detection info is present")
}
