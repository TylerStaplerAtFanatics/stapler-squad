package services

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	sessionv1 "github.com/tstapler/stapler-squad/gen/proto/go/session/v1"
	"github.com/tstapler/stapler-squad/gen/proto/go/session/v1/sessionv1connect"
	"github.com/tstapler/stapler-squad/session/headless"
)

// Compile-time check: HeadlessService must implement HeadlessServiceHandler.
var _ sessionv1connect.HeadlessServiceHandler = (*HeadlessService)(nil)

// HeadlessService implements the RunHeadlessCall streaming RPC.
type HeadlessService struct {
	pool *headless.Pool
}

// NewHeadlessService creates a HeadlessService backed by the given pool.
// pool may be nil; in that case RunHeadlessCall returns CodeUnavailable.
func NewHeadlessService(pool *headless.Pool) *HeadlessService {
	return &HeadlessService{pool: pool}
}

// defaultHeadlessTimeout is applied when RunHeadlessCallRequest.TimeoutSeconds is 0.
const defaultHeadlessTimeout = 900 * time.Second

// maxHeadlessTimeout caps RunHeadlessCallRequest.TimeoutSeconds.
const maxHeadlessTimeout = 1800 * time.Second

// RunHeadlessCall streams LLM output chunks back to the caller.
// It validates the feature_key, applies a timeout, then drains the pool channel.
func (s *HeadlessService) RunHeadlessCall(
	ctx context.Context,
	req *connect.Request[sessionv1.RunHeadlessCallRequest],
	stream *connect.ServerStream[sessionv1.RunHeadlessCallResponse],
) error {
	if s.pool == nil {
		return connect.NewError(connect.CodeUnavailable, fmt.Errorf("headless pool is unavailable (claude binary not found)"))
	}

	featureKey := headless.FeatureKey(req.Msg.FeatureKey)
	if !headless.AllowedFeatureKeys[featureKey] || featureKey == "" {
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid feature_key %q: must be one of review, summarize, acceptance-criteria, pr-description, commit-message, custom", req.Msg.FeatureKey))
	}

	// Apply timeout.
	timeoutSecs := int(req.Msg.TimeoutSeconds)
	timeout := defaultHeadlessTimeout
	if timeoutSecs > 0 {
		timeout = time.Duration(timeoutSecs) * time.Second
	}
	if timeout > maxHeadlessTimeout {
		timeout = maxHeadlessTimeout
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ch, err := s.pool.Call(callCtx, featureKey, req.Msg.SystemPrompt, req.Msg.UserPrompt)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("headless call start: %w", err))
	}

	for chunk := range ch {
		if chunk.Err != nil {
			// Send error chunk and stop.
			if sendErr := stream.Send(&sessionv1.RunHeadlessCallResponse{
				IsError:      true,
				ErrorMessage: chunk.Err.Error(),
				Done:         true,
			}); sendErr != nil {
				return sendErr
			}
			return nil
		}
		resp := &sessionv1.RunHeadlessCallResponse{
			Text: chunk.Text,
			Done: chunk.Done,
		}
		if sendErr := stream.Send(resp); sendErr != nil {
			return sendErr
		}
		if chunk.Done {
			return nil
		}
	}

	// Channel closed without Done=true (context cancelled).
	if ctx.Err() != nil {
		return nil // client disconnected; return nil per WatchSessions pattern
	}

	// Send final done message.
	return stream.Send(&sessionv1.RunHeadlessCallResponse{Done: true})
}
