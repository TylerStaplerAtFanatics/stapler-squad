package headless

import (
	"context"
	"encoding/json"
	"fmt"
)

// Feature key constants for well-known AI features.
const (
	FeatureKeyReview        FeatureKey = "review"
	FeatureKeySummarize     FeatureKey = "summarize"
	FeatureKeyAC            FeatureKey = "acceptance-criteria"
	FeatureKeyPRDescription FeatureKey = "pr-description"
	FeatureKeyCommitMessage FeatureKey = "commit-message"
	FeatureKeyCustom        FeatureKey = "custom"
)

// AllowedFeatureKeys is the set of feature keys accepted by RunHeadlessCall.
var AllowedFeatureKeys = map[FeatureKey]bool{
	FeatureKeyReview:        true,
	FeatureKeySummarize:     true,
	FeatureKeyAC:            true,
	FeatureKeyPRDescription: true,
	FeatureKeyCommitMessage: true,
	FeatureKeyCustom:        true,
}

// maxDiffSizePR is the max byte size for diffs passed to DraftPRDescription.
const maxDiffSizePR = 40_000

// maxDiffSizeCommit is the max byte size for diffs passed to SuggestCommitMessage.
const maxDiffSizeCommit = 20_000

// summarizeSystemPrompt is the stable system prompt for SummarizeBacklogItem.
// Stable prompts enable prefix-caching across repeated calls.
const summarizeSystemPrompt = `You are a backlog analyst. Produce a one-paragraph summary and suggest up to 3 tags. Output JSON: {"summary":"...","tags":[...]}`

// acSystemPrompt is the stable system prompt for GenerateAcceptanceCriteria.
const acSystemPrompt = `You are a product analyst. Output exactly 3-5 acceptance criteria as a JSON array of strings. Each criterion must be testable and specific.`

// prDescriptionSystemPrompt is the stable system prompt for DraftPRDescription.
const prDescriptionSystemPrompt = `You are a technical writer. Draft a pull request description using Conventional Commit conventions. Format: ## Summary, ## Changes, ## Test plan.`

// commitMessageSystemPrompt is the stable system prompt for SuggestCommitMessage.
const commitMessageSystemPrompt = `You are a commit message expert. Output a single Conventional Commit message (type(scope): description). No extra text.`

// reviewSystemPrompt is the stable role/instruction portion of the review prompt.
// This is separated from the per-call data payload (item, diff) to enable prefix-caching.
const reviewSystemPrompt = `You are a code review agent. Your ONLY task is to evaluate the diff against the acceptance criteria and call submit_review_verdict. Do not write any code. Do not modify any files.`

// ReviewSystemPrompt returns the stable system prompt for review gate calls.
// Exported so session/backlog_lifecycle.go can use it without embedding the prompt inline.
func ReviewSystemPrompt() string { return reviewSystemPrompt }

// SummarizeBacklogItem calls the LLM to summarize a backlog item.
// Returns the summary text from the JSON response.
func SummarizeBacklogItem(ctx context.Context, pool *Pool, title, description string) (string, error) {
	userPrompt := fmt.Sprintf("Title: %s\n\nDescription: %s", title, description)
	raw, err := pool.CallBlocking(ctx, FeatureKeySummarize, summarizeSystemPrompt, userPrompt)
	if err != nil {
		return "", fmt.Errorf("SummarizeBacklogItem: %w", err)
	}

	var resp struct {
		Summary string   `json:"summary"`
		Tags    []string `json:"tags"`
	}
	if jsonErr := json.Unmarshal([]byte(raw), &resp); jsonErr != nil {
		// Return raw text as fallback when JSON parsing fails.
		return raw, nil
	}
	return resp.Summary, nil
}

// GenerateAcceptanceCriteria calls the LLM to generate acceptance criteria.
// Returns a slice of criterion strings.
func GenerateAcceptanceCriteria(ctx context.Context, pool *Pool, title, description string) ([]string, error) {
	userPrompt := fmt.Sprintf("Title: %s\n\nDescription: %s", title, description)
	raw, err := pool.CallBlocking(ctx, FeatureKeyAC, acSystemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("GenerateAcceptanceCriteria: %w", err)
	}

	var criteria []string
	if jsonErr := json.Unmarshal([]byte(raw), &criteria); jsonErr != nil {
		return nil, fmt.Errorf("GenerateAcceptanceCriteria: JSON parse error: %w", jsonErr)
	}
	if len(criteria) == 0 {
		return nil, fmt.Errorf("GenerateAcceptanceCriteria: empty criteria list")
	}
	return criteria, nil
}

// DraftPRDescription calls the LLM to draft a pull request description.
// Diffs longer than maxDiffSizePR bytes are truncated before sending.
func DraftPRDescription(ctx context.Context, pool *Pool, diff, branchName string) (string, error) {
	if len(diff) > maxDiffSizePR {
		diff = diff[:maxDiffSizePR]
	}
	userPrompt := fmt.Sprintf("Branch: %s\n\nDiff:\n%s", branchName, diff)
	raw, err := pool.CallBlocking(ctx, FeatureKeyPRDescription, prDescriptionSystemPrompt, userPrompt)
	if err != nil {
		return "", fmt.Errorf("DraftPRDescription: %w", err)
	}
	return raw, nil
}

// SuggestCommitMessage calls the LLM to generate a Conventional Commit message.
// Diffs longer than maxDiffSizeCommit bytes are truncated before sending.
func SuggestCommitMessage(ctx context.Context, pool *Pool, diff string) (string, error) {
	if len(diff) > maxDiffSizeCommit {
		diff = diff[:maxDiffSizeCommit]
	}
	raw, err := pool.CallBlocking(ctx, FeatureKeyCommitMessage, commitMessageSystemPrompt, diff)
	if err != nil {
		return "", fmt.Errorf("SuggestCommitMessage: %w", err)
	}
	return raw, nil
}
