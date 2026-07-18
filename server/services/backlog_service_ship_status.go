package services

// backlog_service_ship_status.go — GetBacklogItemShipStatus, a read-only RPC
// answering "did this item's code actually ship" from durable evidence
// (repo_path + the most recent work session's commit) rather than a live
// per-session worktree. The existing GetVCSStatus RPC requires a live
// in-memory Instance (findInstanceFast) and returns nothing once a session's
// worktree has been cleaned up — which is exactly the normal end state for a
// done item, not a failure. This RPC works from durable data instead.

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	sessionv1 "github.com/tstapler/stapler-squad/gen/proto/go/session/v1"
	"github.com/tstapler/stapler-squad/session"
	"github.com/tstapler/stapler-squad/session/ent"
	"github.com/tstapler/stapler-squad/session/git"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GetBacklogItemShipStatus reports whether itemID's code actually landed on
// main, plus the branch's position relative to main when the branch still
// exists.
// +api: backlog:get-item-ship-status
func (s *BacklogService) GetBacklogItemShipStatus(
	ctx context.Context,
	req *connect.Request[sessionv1.GetBacklogItemShipStatusRequest],
) (*connect.Response[sessionv1.GetBacklogItemShipStatusResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	item, err := s.storage.GetBacklogItem(ctx, req.Msg.ItemId)
	if err != nil {
		if ent.IsNotFound(err) || errors.Is(err, session.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("backlog item %q not found", req.Msg.ItemId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get backlog item: %w", err))
	}

	itemSessions, err := s.storage.ListItemSessions(ctx, req.Msg.ItemId)
	if err != nil {
		return connect.NewResponse(&sessionv1.GetBacklogItemShipStatusResponse{
			Status: &sessionv1.BacklogItemShipStatus{Error: fmt.Sprintf("failed to load item sessions: %v", err)},
		}), nil
	}

	var lastWork *session.ItemSessionSummary
	for i := range itemSessions {
		// Ascending by CreatedAt (ListItemSessions' query order) — keep
		// overwriting so this ends up holding the *most recent* work session.
		if itemSessions[i].Role == session.SessionRoleWork {
			lastWork = &itemSessions[i]
		}
	}
	if lastWork == nil || lastWork.LastCommitSha == "" {
		return connect.NewResponse(&sessionv1.GetBacklogItemShipStatusResponse{
			Status: &sessionv1.BacklogItemShipStatus{Error: "no work session ever committed code for this item"},
		}), nil
	}

	status := &sessionv1.BacklogItemShipStatus{
		PrUrl:             item.PrURL,
		LastCommitSha:     lastWork.LastCommitSha,
		LastCommitMessage: lastWork.LastCommitMessage,
	}
	if lastWork.LastCommitAt != nil {
		status.LastCommitAt = timestamppb.New(*lastWork.LastCommitAt)
	}

	onMain, mainErr := git.IsCommitOnMain(item.RepoPath, prFixMainBranch, lastWork.LastCommitSha)
	if mainErr != nil {
		status.Error = fmt.Sprintf("failed to verify commit on main: %v", mainErr)
		return connect.NewResponse(&sessionv1.GetBacklogItemShipStatusResponse{Status: status}), nil
	}
	status.Shipped = onMain
	if onMain {
		if item.PrURL != "" {
			status.ShippedVia = "pr"
		} else {
			status.ShippedVia = "direct"
		}
	}

	if wt, wtErr := s.storage.GetWorktreeDataBySessionUUID(ctx, lastWork.SessionUUID); wtErr == nil && wt.BranchName != "" {
		status.BranchName = wt.BranchName
		branchStatus, branchErr := git.BranchAheadBehind(item.RepoPath, wt.BranchName, prFixMainBranch)
		if branchErr != nil {
			status.Error = fmt.Sprintf("failed to check branch status: %v", branchErr)
		} else {
			status.BranchExists = branchStatus.BranchExists
			status.AheadOfMain = int32(branchStatus.AheadOfMain) //#nosec G115 -- bounded by countCommitsNotAncestorOfCap (500)
			status.BehindMain = int32(branchStatus.BehindMain)   //#nosec G115 -- bounded by countCommitsNotAncestorOfCap (500)
		}
	}

	return connect.NewResponse(&sessionv1.GetBacklogItemShipStatusResponse{Status: status}), nil
}
