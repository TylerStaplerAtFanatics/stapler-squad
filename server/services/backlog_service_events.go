package services

// backlog_service_events.go — WatchBacklogItems streaming RPC handler for
// BacklogService.
//
// NOTE: This currently contains only a CodeUnimplemented placeholder so the
// service satisfies sessionv1connect.BacklogServiceHandler after the
// WatchBacklogItems RPC was added to the proto (project_plans/
// backlog-event-driven-updates Epic 1.1). The real event-bus-backed
// implementation (snapshot + live stream, after_seq replay) is Epic 3.1
// (Story 3.1.1) of that plan — out of scope here.

import (
	"context"

	"connectrpc.com/connect"
	sessionv1 "github.com/tstapler/stapler-squad/gen/proto/go/session/v1"
)

// WatchBacklogItems streams real-time backlog item events. Placeholder until
// Epic 3.1 wires it to the event bus.
func (s *BacklogService) WatchBacklogItems(
	ctx context.Context,
	req *connect.Request[sessionv1.WatchBacklogItemsRequest],
	stream *connect.ServerStream[sessionv1.BacklogItemEvent],
) error {
	return connect.NewError(connect.CodeUnimplemented, nil)
}
