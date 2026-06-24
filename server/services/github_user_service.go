package services

import (
	"context"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/tstapler/stapler-squad/gen/proto/go/session/v1/sessionv1connect"
	sessionv1 "github.com/tstapler/stapler-squad/gen/proto/go/session/v1"
	githubpkg "github.com/tstapler/stapler-squad/github"
	"github.com/tstapler/stapler-squad/log"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Compile-time check: GitHubUserService must implement the generated handler.
var _ sessionv1connect.GitHubUserServiceHandler = (*GitHubUserService)(nil)

// GitHubUserService implements the ConnectRPC GitHubUserServiceHandler.
// Data is served from the in-process UserPRCache (background GraphQL poller).
// No gh subprocess calls are made from this service.
type GitHubUserService struct {
	cache *githubpkg.UserPRCache
}

// NewGitHubUserService creates a new service backed by the given UserPRCache.
func NewGitHubUserService(cache *githubpkg.UserPRCache) *GitHubUserService {
	return &GitHubUserService{cache: cache}
}

// ListUserPRs returns all open PRs from the cache.
func (s *GitHubUserService) ListUserPRs(
	ctx context.Context,
	_ *connect.Request[sessionv1.ListUserPRsRequest],
) (*connect.Response[sessionv1.ListUserPRsResponse], error) {
	authState, username := s.resolveAuthState(ctx)

	prs := s.cache.GetAll()
	protoPRs := make([]*sessionv1.UserPR, 0, len(prs))
	for _, pr := range prs {
		protoPRs = append(protoPRs, userPRToProto(pr))
	}

	return connect.NewResponse(&sessionv1.ListUserPRsResponse{
		Prs: protoPRs,
		AuthState: &sessionv1.GitHubAuthState{
			Available: authState,
			Username:  username,
		},
	}), nil
}

// WatchUserPRs streams UserPREvent messages. The first event is a full snapshot;
// subsequent events fire whenever the UserPRCache refreshes.
func (s *GitHubUserService) WatchUserPRs(
	ctx context.Context,
	_ *connect.Request[sessionv1.WatchUserPRsRequest],
	stream *connect.ServerStream[sessionv1.UserPREvent],
) error {
	authState, username := s.resolveAuthState(ctx)
	protoAuthState := &sessionv1.GitHubAuthState{Available: authState, Username: username}

	// 1. Send initial snapshot.
	prs := s.cache.GetAll()
	protoPRs := make([]*sessionv1.UserPR, 0, len(prs))
	for _, pr := range prs {
		protoPRs = append(protoPRs, userPRToProto(pr))
	}
	if err := stream.Send(&sessionv1.UserPREvent{
		EventType: "snapshot",
		Prs:       protoPRs,
		AuthState: protoAuthState,
	}); err != nil {
		return fmt.Errorf("send initial snapshot: %w", err)
	}

	// 2. Register an update channel. UserPRCache calls onUpdated in a goroutine;
	//    we relay via a buffered channel so the callback never blocks.
	updateCh := make(chan []githubpkg.UserPR, 4)
	s.cache.SetOnUpdated(func(updated []githubpkg.UserPR) {
		select {
		case updateCh <- updated:
		default:
			log.Warn("WatchUserPRs: update channel full, dropping event")
		}
	})
	defer s.cache.SetOnUpdated(nil)

	// 3. Forward updates until client disconnects or ctx is cancelled.
	for {
		select {
		case <-ctx.Done():
			return nil
		case updated, ok := <-updateCh:
			if !ok {
				return nil
			}
			protoUpdated := make([]*sessionv1.UserPR, 0, len(updated))
			for _, pr := range updated {
				protoUpdated = append(protoUpdated, userPRToProto(pr))
			}
			if err := stream.Send(&sessionv1.UserPREvent{
				EventType: "snapshot",
				Prs:       protoUpdated,
				AuthState: protoAuthState,
			}); err != nil {
				return fmt.Errorf("send update: %w", err)
			}
		}
	}
}

// GetGitHubAuthState returns auth availability and the authenticated username.
func (s *GitHubUserService) GetGitHubAuthState(
	ctx context.Context,
	_ *connect.Request[sessionv1.GetGitHubAuthStateRequest],
) (*connect.Response[sessionv1.GetGitHubAuthStateResponse], error) {
	available, username := s.resolveAuthState(ctx)
	errMsg := ""
	if !available {
		errMsg = "GitHub is not authenticated. Set GITHUB_TOKEN or run 'gh auth login'."
	}
	return connect.NewResponse(&sessionv1.GetGitHubAuthStateResponse{
		AuthState: &sessionv1.GitHubAuthState{
			Available:    available,
			Username:     username,
			ErrorMessage: errMsg,
		},
	}), nil
}

// resolveAuthState calls GetCurrentUserLogin to determine auth status.
// Returns (available=false, "") rather than an error on failure so callers
// can degrade gracefully.
func (s *GitHubUserService) resolveAuthState(ctx context.Context) (available bool, username string) {
	authCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	login, err := githubpkg.GetCurrentUserLogin(authCtx)
	if err != nil || login == "" {
		return false, ""
	}
	return true, login
}

// userPRToProto converts a github.UserPR to its proto representation.
func userPRToProto(pr githubpkg.UserPR) *sessionv1.UserPR {
	proto := &sessionv1.UserPR{
		Owner:             pr.Owner,
		Repo:              pr.Repo,
		Number:            int32(pr.Number),
		Title:             pr.Title,
		HtmlUrl:           pr.URL,
		State:             pr.State,
		HeadRef:           pr.HeadRef,
		BaseRef:           pr.BaseRef,
		IsDraft:           pr.IsDraft,
		CheckConclusion:   pr.CheckConclusion,
		ApprovedCount:     int32(pr.ApprovedCount),
		ChangesReqCount:   int32(pr.ChangesReqCount),
		SessionIds:        pr.SessionIDs,
		LocalWorktreePath: pr.LocalWorktreePath,
	}
	if !pr.UpdatedAt.IsZero() {
		proto.UpdatedAt = timestamppb.New(pr.UpdatedAt)
	}
	if !pr.ClosedAt.IsZero() {
		proto.ClosedAt = timestamppb.New(pr.ClosedAt)
	}
	if !pr.MergedAt.IsZero() {
		proto.MergedAt = timestamppb.New(pr.MergedAt)
	}
	return proto
}
