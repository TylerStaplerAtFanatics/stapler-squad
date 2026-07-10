package services

// backlog_service_lifecycle.go — state-mutation RPC handlers for BacklogService.
// These handlers create, update, archive, delete, or transition backlog items and sources.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"connectrpc.com/connect"
	sessionv1 "github.com/tstapler/stapler-squad/gen/proto/go/session/v1"
	"github.com/tstapler/stapler-squad/config"
	"github.com/tstapler/stapler-squad/log"
	"github.com/tstapler/stapler-squad/session"
	"github.com/tstapler/stapler-squad/session/ent"
)

// encryptAndMergeToken produces a token config JSON string suitable for storage.
// If key is non-nil the token is AES-GCM encrypted and the result is
// `{"token":"<ciphertext>","encrypted":true}`. Otherwise the token is stored
// unencrypted (backwards-compat). The returned string can be stored as-is when
// the existing config is empty. When existingConfig is non-empty the token JSON
// is merged into it (token fields win). Returns the merged JSON or an error.
func encryptAndMergeToken(cfg *config.Config, token, existingConfig string) (string, error) {
	var tokenJSON string
	if cfg != nil {
		key, err := cfg.GetOrCreateEncryptionKey()
		if err != nil {
			return "", fmt.Errorf("get encryption key: %w", err)
		}
		encrypted, err := session.EncryptToken(key, token)
		if err != nil {
			return "", fmt.Errorf("encrypt token: %w", err)
		}
		tokenJSON = fmt.Sprintf(`{"token":%q,"encrypted":true}`, encrypted)
	} else {
		// No config available; store unencrypted (backwards compatibility).
		tokenJSON = fmt.Sprintf(`{"token":%q}`, token)
	}

	if existingConfig == "" {
		return tokenJSON, nil
	}

	// Merge token fields into the existing config JSON.
	var cfgMap map[string]interface{}
	if err := json.Unmarshal([]byte(existingConfig), &cfgMap); err != nil {
		return "", fmt.Errorf("unmarshal existing config: %w", err)
	}
	var tokMap map[string]interface{}
	if err := json.Unmarshal([]byte(tokenJSON), &tokMap); err != nil {
		return "", fmt.Errorf("unmarshal token json: %w", err)
	}
	for k, v := range tokMap {
		cfgMap[k] = v
	}
	merged, err := json.Marshal(cfgMap)
	if err != nil {
		return "", fmt.Errorf("marshal merged config: %w", err)
	}
	return string(merged), nil
}

// acCriteriaToJSON serializes proto AcCriterion slice to JSON string for storage.
func acCriteriaToJSON(protoAC []*sessionv1.AcCriterion) (session.AcCriteriaJSON, error) {
	if len(protoAC) == 0 {
		return "", nil
	}
	criteria := make([]session.AcCriterion, len(protoAC))
	for i, c := range protoAC {
		criteria[i] = session.AcCriterion{
			Index:  int(c.Index),
			Text:   c.Text,
			Status: session.AcStatus(c.Status),
		}
	}
	b, err := json.Marshal(criteria)
	if err != nil {
		return "", err
	}
	return session.AcCriteriaJSON(b), nil
}

// --- CreateBacklogItem ---

// CreateBacklogItem adds a new item to the backlog.
// +api: backlog:create-item
func (s *BacklogService) CreateBacklogItem(
	ctx context.Context,
	req *connect.Request[sessionv1.CreateBacklogItemRequest],
) (*connect.Response[sessionv1.CreateBacklogItemResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}
	if req.Msg.Title == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("title is required"))
	}

	acJSON, err := acCriteriaToJSON(req.Msg.AcceptanceCriteria)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid acceptance_criteria: %w", err))
	}

	priority := int(req.Msg.Priority)
	if priority == 0 {
		priority = session.DefaultBacklogPriority
	}

	repoPath, err := s.resolveRepoPathInput(req.Msg.RepoPath)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	data := session.BacklogItemData{
		Title:              req.Msg.Title,
		Description:        req.Msg.Description,
		AcceptanceCriteria: acJSON,
		Priority:           priority,
		Status:             string(session.BacklogStatusIdea),
		RepoPath:           repoPath,
		SkipReviewGate:     req.Msg.SkipReviewGate,
		SkipPlanning:       req.Msg.SkipPlanning,
		Notes:              req.Msg.Notes,
	}

	created, err := s.storage.CreateBacklogItem(ctx, data)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create backlog item: %w", err))
	}

	triageTriggered := false
	if !req.Msg.SkipTriage && created.RepoPath != "" && s.headlessPool != nil {
		// 30s gates only the synchronous path (item lookup + ItemSession creation).
		// The headless LLM call itself runs in a goroutine under shutdownCtx (30-min cap).
		triageCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_, triageErr := s.TriggerTriage(triageCtx,
			connect.NewRequest(&sessionv1.TriggerTriageRequest{ItemId: created.ID}))
		if triageErr != nil {
			log.WarningLog.Printf("[CreateBacklogItem] auto-triage failed for item %s: %v", created.ID, triageErr)
			// Do not fail the create; log and continue
		} else {
			triageTriggered = true
		}
	}

	return connect.NewResponse(&sessionv1.CreateBacklogItemResponse{
		Item:            backlogItemToProto(created, s.buildCostLookup()),
		TriageTriggered: triageTriggered,
	}), nil
}

// --- UpdateBacklogItem ---

// UpdateBacklogItem modifies the properties of an existing backlog item.
// +api: backlog:update-item
func (s *BacklogService) UpdateBacklogItem(
	ctx context.Context,
	req *connect.Request[sessionv1.UpdateBacklogItemRequest],
) (*connect.Response[sessionv1.UpdateBacklogItemResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	acJSON, err := acCriteriaToJSON(req.Msg.AcceptanceCriteria)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid acceptance_criteria: %w", err))
	}

	update := session.BacklogItemUpdate{}
	if req.Msg.Title != "" {
		title := req.Msg.Title
		update.Title = &title
	}
	if req.Msg.Description != "" {
		desc := req.Msg.Description
		update.Description = &desc
	}
	if acJSON != "" {
		update.AcceptanceCriteria = &acJSON
	}
	if req.Msg.Priority != 0 {
		prio := int(req.Msg.Priority)
		update.Priority = &prio
	}
	if req.Msg.RepoPath != "" {
		rp, resolveErr := s.resolveRepoPathInput(req.Msg.RepoPath)
		if resolveErr != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, resolveErr)
		}
		update.RepoPath = &rp
	}
	skipRG := req.Msg.SkipReviewGate
	update.SkipReviewGate = &skipRG
	skipP := req.Msg.SkipPlanning
	update.SkipPlanning = &skipP
	if req.Msg.Notes != "" {
		notes := req.Msg.Notes
		update.Notes = &notes
	}

	var precondition *session.BacklogItemPrecondition
	if req.Msg.ExpectedStatus != "" || req.Msg.ExpectedUpdatedAt != nil {
		precondition = &session.BacklogItemPrecondition{
			ExpectedStatus: req.Msg.ExpectedStatus,
		}
		if req.Msg.ExpectedUpdatedAt != nil {
			t := req.Msg.ExpectedUpdatedAt.AsTime()
			precondition.ExpectedUpdatedAt = &t
		}
	}

	updated, err := s.storage.UpdateBacklogItem(ctx, req.Msg.ItemId, update, precondition)
	if err != nil {
		if errors.Is(err, session.ErrPreconditionFailed) {
			return nil, connect.NewError(connect.CodeAborted, err)
		}
		if ent.IsNotFound(err) || errors.Is(err, session.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("backlog item %q not found", req.Msg.ItemId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update backlog item: %w", err))
	}

	return connect.NewResponse(&sessionv1.UpdateBacklogItemResponse{
		Item: backlogItemToProto(updated, s.buildCostLookup()),
	}), nil
}

// --- ArchiveBacklogItem ---

// ArchiveBacklogItem soft-deletes an item by setting its archived_at timestamp.
// +api: backlog:archive-item
func (s *BacklogService) ArchiveBacklogItem(
	ctx context.Context,
	req *connect.Request[sessionv1.ArchiveBacklogItemRequest],
) (*connect.Response[sessionv1.ArchiveBacklogItemResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	// Push work branches before archiving so changes are durable.
	if sessions, lsErr := s.storage.ListItemSessions(ctx, req.Msg.ItemId); lsErr == nil {
		s.commitAndPushItemWorktrees(ctx, sessions)
	}

	archived, err := s.storage.ArchiveBacklogItem(ctx, req.Msg.ItemId)
	if err != nil {
		if ent.IsNotFound(err) || errors.Is(err, session.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("backlog item %q not found", req.Msg.ItemId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to archive backlog item: %w", err))
	}

	// Best-effort: clean up git worktrees for work sessions on archive.
	if sessions, lsErr := s.storage.ListItemSessions(ctx, req.Msg.ItemId); lsErr == nil {
		s.cleanupItemWorktrees(ctx, sessions)
	}

	return connect.NewResponse(&sessionv1.ArchiveBacklogItemResponse{
		Item: backlogItemToProto(archived, s.buildCostLookup()),
	}), nil
}

// --- DeleteBacklogItem ---

// DeleteBacklogItem permanently removes an item and all its child records.
// +api: backlog:delete-item
func (s *BacklogService) DeleteBacklogItem(
	ctx context.Context,
	req *connect.Request[sessionv1.DeleteBacklogItemRequest],
) (*connect.Response[sessionv1.DeleteBacklogItemResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	if err := s.storage.DeleteBacklogItem(ctx, req.Msg.ItemId); err != nil {
		if ent.IsNotFound(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("backlog item %q not found", req.Msg.ItemId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to delete backlog item: %w", err))
	}

	return connect.NewResponse(&sessionv1.DeleteBacklogItemResponse{}), nil
}

// --- TransitionBacklogItemStatus ---

// TransitionBacklogItemStatus moves an item through the status state machine.
// +api: backlog:transition-status
func (s *BacklogService) TransitionBacklogItemStatus(
	ctx context.Context,
	req *connect.Request[sessionv1.TransitionBacklogItemStatusRequest],
) (*connect.Response[sessionv1.TransitionBacklogItemStatusResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	// Load current item to check CanTransitionBacklog.
	item, err := s.storage.GetBacklogItem(ctx, req.Msg.ItemId)
	if err != nil {
		if ent.IsNotFound(err) || errors.Is(err, session.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("backlog item %q not found", req.Msg.ItemId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get backlog item: %w", err))
	}

	from := session.BacklogStatus(item.Status)
	to := session.BacklogStatus(req.Msg.TargetStatus)

	if !s.engine.CanTransition(from, to) {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid transition from %q to %q", from, to))
	}

	// Load the most recent ReviewVerdict for this item so TransitionGuard can
	// evaluate the review→done guard (ErrVerdictRequired).
	overallOutcome, verdictErr := s.storage.GetMostRecentReviewVerdictForItem(ctx, req.Msg.ItemId)
	if verdictErr != nil {
		log.WarningLog.Printf("[TransitionBacklogItemStatus] failed to load review verdict for item %s: %v", req.Msg.ItemId, verdictErr)
		// Non-fatal: proceed with empty outcome; TransitionGuard will block review→done if needed.
	}

	// Run transition guard for business rules.
	guardInput := session.BacklogItemTransitionInput{
		Status:            from,
		AcCriteria:        item.AcceptanceCriteria,
		PlanApproved:      item.PlanApproved,
		SkipPlanning:      item.SkipPlanning,
		PlanArtifactsPath: item.PlanArtifactsPath,
		OverallOutcome:    overallOutcome,
		OverrideReason:    req.Msg.OverrideReason,
	}
	if guardErr := s.engine.ValidateGates(guardInput, to); guardErr != nil {
		if errors.Is(guardErr, session.ErrACRequired) ||
			errors.Is(guardErr, session.ErrPlanRequired) ||
			errors.Is(guardErr, session.ErrPlanArtifactsRequired) ||
			errors.Is(guardErr, session.ErrVerdictRequired) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, guardErr)
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, guardErr)
	}

	var precondition *session.BacklogItemPrecondition
	if req.Msg.ExpectedStatus != "" || req.Msg.ExpectedUpdatedAt != nil {
		precondition = &session.BacklogItemPrecondition{
			ExpectedStatus: req.Msg.ExpectedStatus,
		}
		if req.Msg.ExpectedUpdatedAt != nil {
			t := req.Msg.ExpectedUpdatedAt.AsTime()
			precondition.ExpectedUpdatedAt = &t
		}
	}

	// Push work branches before marking done so changes are durable before the
	// status changes and the worktree is removed.
	if to == session.BacklogStatusDone {
		if sessions, lsErr := s.storage.ListItemSessions(ctx, req.Msg.ItemId); lsErr == nil {
			s.commitAndPushItemWorktrees(ctx, sessions)
		}
	}

	updated, err := s.storage.TransitionBacklogItemStatus(ctx, req.Msg.ItemId, to, precondition)
	if err != nil {
		if errors.Is(err, session.ErrPreconditionFailed) {
			return nil, connect.NewError(connect.CodeAborted, err)
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to transition backlog item: %w", err))
	}

	// Best-effort: clean up git worktrees for work sessions on terminal transitions.
	if to == session.BacklogStatusDone || to == session.BacklogStatusArchived {
		if sessions, lsErr := s.storage.ListItemSessions(ctx, req.Msg.ItemId); lsErr == nil {
			s.cleanupItemWorktrees(ctx, sessions)
		}
	}

	// Backward to idea/refining: reset planning approval so triage must re-run.
	// Best-effort — a warning is logged but the transition itself is already committed.
	if to == session.BacklogStatusIdea || to == session.BacklogStatusRefining {
		planApproved := false
		planArtifactsPath := ""
		if upd, resetErr := s.storage.UpdateBacklogItem(ctx, req.Msg.ItemId, session.BacklogItemUpdate{
			PlanApproved:      &planApproved,
			PlanArtifactsPath: &planArtifactsPath,
		}, nil); resetErr != nil {
			log.WarningLog.Printf("[TransitionBacklogItemStatus] failed to reset planning state for item %s: %v", req.Msg.ItemId, resetErr)
		} else {
			updated = upd
		}
	}

	return connect.NewResponse(&sessionv1.TransitionBacklogItemStatusResponse{
		Item: backlogItemToProto(updated, s.buildCostLookup()),
	}), nil
}

// --- ApprovePlan ---

// ApprovePlan marks the planning artifacts for an item as approved.
// +api: backlog:approve-plan
func (s *BacklogService) ApprovePlan(
	ctx context.Context,
	req *connect.Request[sessionv1.ApprovePlanRequest],
) (*connect.Response[sessionv1.ApprovePlanResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	item, err := s.storage.GetBacklogItem(ctx, req.Msg.ItemId)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("backlog item %q not found", req.Msg.ItemId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get backlog item: %w", err))
	}

	if item.PlanArtifactsPath == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("no plan artifacts found — run TriggerTriage first"))
	}
	if _, statErr := os.Stat(item.PlanArtifactsPath); statErr != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("plan artifacts path %q does not exist on disk — re-run TriggerTriage", item.PlanArtifactsPath))
	}

	now := time.Now()
	approved := true
	update := session.BacklogItemUpdate{
		PlanApproved:   &approved,
		PlanApprovedAt: &now,
	}

	updated, err := s.storage.UpdateBacklogItem(ctx, req.Msg.ItemId, update, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to approve plan: %w", err))
	}

	return connect.NewResponse(&sessionv1.ApprovePlanResponse{
		Item: backlogItemToProto(updated, s.buildCostLookup()),
	}), nil
}

// --- ItemSource handlers ---

// CreateItemSource registers a new external plugin source.
// +api: backlog:create-source
func (s *BacklogService) CreateItemSource(
	ctx context.Context,
	req *connect.Request[sessionv1.CreateItemSourceRequest],
) (*connect.Response[sessionv1.CreateItemSourceResponse], error) {
	if s.sourceBackend == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	data := session.ItemSourceData{
		PluginID:    req.Msg.PluginId,
		DisplayName: req.Msg.DisplayName,
		Enabled:     true,
		Config:      req.Msg.ConfigJson,
	}
	if req.Msg.Token != "" {
		data.TokenConfigured = true
		merged, mergeErr := encryptAndMergeToken(s.cfg, req.Msg.Token, data.Config)
		if mergeErr != nil {
			return nil, connect.NewError(connect.CodeInternal, mergeErr)
		}
		data.Config = merged
	}

	created, err := s.sourceBackend.CreateItemSource(ctx, data)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create item source: %w", err))
	}

	return connect.NewResponse(&sessionv1.CreateItemSourceResponse{
		Source: itemSourceToProto(created),
	}), nil
}

// UpdateItemSource modifies configuration for an existing item source.
// +api: backlog:update-source
func (s *BacklogService) UpdateItemSource(
	ctx context.Context,
	req *connect.Request[sessionv1.UpdateItemSourceRequest],
) (*connect.Response[sessionv1.UpdateItemSourceResponse], error) {
	if s.sourceBackend == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	update := session.ItemSourceUpdate{}
	if req.Msg.DisplayName != "" {
		dn := req.Msg.DisplayName
		update.DisplayName = &dn
	}
	enabled := req.Msg.Enabled
	update.Enabled = &enabled
	if req.Msg.Token != "" {
		// UpdateItemSource replaces the config wholesale (no prior config to merge).
		tokenJSON, mergeErr := encryptAndMergeToken(s.cfg, req.Msg.Token, "")
		if mergeErr != nil {
			return nil, connect.NewError(connect.CodeInternal, mergeErr)
		}
		update.Config = &tokenJSON
	}

	updated, err := s.sourceBackend.UpdateItemSource(ctx, req.Msg.SourceId, update)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("item source %q not found", req.Msg.SourceId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update item source: %w", err))
	}

	return connect.NewResponse(&sessionv1.UpdateItemSourceResponse{
		Source: itemSourceToProto(updated),
	}), nil
}

// DeleteItemSource removes an external item source registration.
// +api: backlog:delete-source
func (s *BacklogService) DeleteItemSource(
	ctx context.Context,
	req *connect.Request[sessionv1.DeleteItemSourceRequest],
) (*connect.Response[sessionv1.DeleteItemSourceResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	if err := s.storage.DeleteItemSource(ctx, req.Msg.SourceId); err != nil {
		if ent.IsNotFound(err) {
			return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("item source %q not found", req.Msg.SourceId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to delete item source: %w", err))
	}

	return connect.NewResponse(&sessionv1.DeleteItemSourceResponse{}), nil
}

// --- Session-linked handlers ---

// SpawnSessionFromItem creates a new AI agent session for a backlog item.
// +api: backlog:spawn-session

// OverrideVerdict manually overrides a review verdict for an item session.
// +api: backlog:override-verdict
func (s *BacklogService) OverrideVerdict(
	ctx context.Context,
	req *connect.Request[sessionv1.OverrideVerdictRequest],
) (*connect.Response[sessionv1.OverrideVerdictResponse], error) {
	if s.storage == nil {
		return nil, connect.NewError(connect.CodeUnavailable, fmt.Errorf("storage not available"))
	}

	// 1. Validate override reason.
	if req.Msg.OverrideReason == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("override_reason is required"))
	}

	// 2. Load the ItemSession by entity UUID to get the linked BacklogItem ID.
	is, err := s.storage.GetItemSession(ctx, req.Msg.ItemSessionId)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound,
				fmt.Errorf("item session %q not found", req.Msg.ItemSessionId))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get item session: %w", err))
	}

	// Load the linked BacklogItem (edge is loaded via GetItemSession).
	var itemID string
	if is.Edges.BacklogItem != nil {
		itemID = is.Edges.BacklogItem.ID.String()
	} else {
		return nil, connect.NewError(connect.CodeInternal,
			fmt.Errorf("item session %q has no linked backlog item", req.Msg.ItemSessionId))
	}

	// 3. Determine outcome based on to_status.
	outcome := session.ReviewVerdictPass
	if req.Msg.ToStatus == string(session.BacklogStatusInProgress) {
		outcome = session.ReviewVerdictFail
	}

	// 4. Save/upsert the ReviewVerdict with override fields.
	now := time.Now()
	if _, verdictErr := s.storage.SaveReviewVerdict(ctx, is.ID.String(), session.ReviewVerdictData{
		OverallOutcome: outcome,
		Summary:        fmt.Sprintf("Manual override: %s", req.Msg.OverrideReason),
		OverrideBy:     "user",
		OverrideReason: req.Msg.OverrideReason,
		OverrideAt:     &now,
	}); verdictErr != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to save review verdict: %w", verdictErr))
	}

	// 5. Transition item to target status if valid (validate via state machine).
	var updatedItem *session.BacklogItemData
	if req.Msg.ToStatus != "" {
		toStatus := session.BacklogStatus(req.Msg.ToStatus)
		currentItem, currentErr := s.storage.GetBacklogItem(ctx, itemID)
		if currentErr != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load item for transition: %w", currentErr))
		}
		from := session.BacklogStatus(currentItem.Status)
		if !session.CanTransitionBacklog(from, toStatus) {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("cannot transition item from %q to %q", from, toStatus))
		}
		updated, transErr := s.storage.TransitionBacklogItemStatus(ctx, itemID, toStatus, nil)
		if transErr != nil {
			log.ErrorLog.Printf("[OverrideVerdict] failed to transition item %s to %s: %v", itemID, toStatus, transErr)
		} else {
			updatedItem = updated
		}
	}

	// Fall back to loading item if transition was skipped or failed.
	if updatedItem == nil {
		updatedItem, err = s.storage.GetBacklogItem(ctx, itemID)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to reload backlog item: %w", err))
		}
	}

	return connect.NewResponse(&sessionv1.OverrideVerdictResponse{
		Item: backlogItemToProto(updatedItem, s.buildCostLookup()),
	}), nil
}
