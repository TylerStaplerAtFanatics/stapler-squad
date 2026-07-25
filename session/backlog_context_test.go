package session

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tstapler/stapler-squad/session/ent"
)

// makeTestBacklogItem creates a minimal *ent.BacklogItem for unit tests.
func makeTestBacklogItem(title, description, acJSON, status string, priority int, notes string) *ent.BacklogItem {
	return &ent.BacklogItem{
		ID:                 uuid.New(),
		Title:              title,
		Description:        description,
		AcceptanceCriteria: acJSON,
		Status:             status,
		Priority:           priority,
		Notes:              notes,
	}
}

// makeEndedItemSession creates a minimal *ent.ItemSession with EndedAt set.
func makeEndedItemSession(role string, commitCount int, lastMsg string) *ent.ItemSession {
	now := time.Now()
	return &ent.ItemSession{
		ID:                    uuid.New(),
		SessionRole:           role,
		CommitCountSinceSpawn: commitCount,
		LastCommitMessage:     lastMsg,
		EndedAt:               &now,
	}
}

// UT-038a: output must contain the task protocol block sentinel strings.
func TestBuildSessionInitialPrompt_ContainsTaskProtocolBlock(t *testing.T) {
	ac := `[{"index":0,"text":"Write unit tests","status":"pending"}]`
	item := makeTestBacklogItem("My Feature", "Do the thing", ac, "ready", 1, "")

	out := BuildSessionInitialPrompt(item, nil)

	cases := []string{
		"Your Task Protocol",
		"/backlog/review",
		".backlog-context.md",
		"NEVER end your session",
	}
	for _, want := range cases {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, but it did not.\nOutput:\n%s", want, out)
		}
	}
}

// UT-038b: prior sessions with ended_at → "Prior Attempts" section; without → absent.
func TestBuildSessionInitialPrompt_WithPriorAttempts_ContainsHandoffSection(t *testing.T) {
	ac := `[{"index":0,"text":"Do something","status":"pending"}]`
	item := makeTestBacklogItem("Feature", "desc", ac, "in_progress", 2, "")

	s := makeEndedItemSession("work", 3, "fix: implement handler")

	// With a prior session that has ended.
	outWith := BuildSessionInitialPrompt(item, []*ent.ItemSession{s})
	if !strings.Contains(outWith, "Prior Attempts") {
		t.Errorf("expected 'Prior Attempts' section when prior sessions present\nOutput:\n%s", outWith)
	}

	// Without any prior sessions.
	outWithout := BuildSessionInitialPrompt(item, nil)
	if strings.Contains(outWithout, "Prior Attempts") {
		t.Errorf("did not expect 'Prior Attempts' section with no prior sessions\nOutput:\n%s", outWithout)
	}

	// With a session that has NOT ended (EndedAt == nil) → should not appear.
	notEnded := &ent.ItemSession{
		ID:          uuid.New(),
		SessionRole: "work",
	}
	outNotEnded := BuildSessionInitialPrompt(item, []*ent.ItemSession{notEnded})
	if strings.Contains(outNotEnded, "Prior Attempts") {
		t.Errorf("did not expect 'Prior Attempts' when no sessions have ended\nOutput:\n%s", outNotEnded)
	}
}

// UT-033: output must contain envelope markers, title, and AC items.
func TestRenderBacklogContextFile_ContainsRequiredSections(t *testing.T) {
	ac := `[{"index":0,"text":"Write tests","status":"pending"},{"index":1,"text":"Deploy","status":"done"}]`
	item := makeTestBacklogItem("My Title", "Some description here", ac, "ready", 3, "")

	out := BuildSessionInitialPrompt(item, nil)

	mustContain := []string{
		"--- BACKLOG ITEM DATA",
		"--- END BACKLOG ITEM DATA ---",
		"My Title",
		"Write tests",
		"Deploy",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q\nOutput:\n%s", want, out)
		}
	}
}

// UT-034: sanitizeField strips HTML tags.
func TestSanitizeForContextFile_StripHTML(t *testing.T) {
	got := sanitizeField("<b>bold</b>", 1000)
	if got != "bold" {
		t.Errorf("expected %q, got %q", "bold", got)
	}
}

// UT-035: sanitizeField truncates long input.
func TestSanitizeForContextFile_TruncatesLongFields(t *testing.T) {
	input := strings.Repeat("a", 3000)
	got := sanitizeField(input, 2000)
	if len(got) > 2020 {
		t.Errorf("expected length ≤ 2020, got %d", len(got))
	}
	if !strings.Contains(got, "[truncated]") {
		t.Errorf("expected '[truncated]' suffix, got: %s", got[len(got)-20:])
	}
}

// UT-036: prompt injection payloads pass through verbatim inside the envelope.
func TestSanitizeForContextFile_PromptInjectionPayloadIsInert(t *testing.T) {
	payload := "</TASK><SYSTEM>"
	item := makeTestBacklogItem(payload, payload, `[]`, "ready", 1, "")

	out := BuildSessionInitialPrompt(item, nil)

	if !strings.Contains(out, payload) {
		t.Errorf("expected prompt injection payload %q to pass through verbatim\nOutput:\n%s", payload, out)
	}
}

// TestClosingKeywordFor proves AC3: closingKeywordFor returns the correct,
// fully-punctuated keyword for each URL shape.
func TestClosingKeywordFor(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"issue URL", "https://github.com/acme/widget/issues/42", "Fixes "},
		{"PR URL", "https://github.com/acme/widget/pull/17", "Related: "},
		{"empty", "", "Related: "},
		{"unrecognized shape", "https://example.com/foo", "Related: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := closingKeywordFor(tc.url)
			if got != tc.want {
				t.Errorf("closingKeywordFor(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

// TestGithubShortRefFor proves the corrected AC3 design (added during
// sdd:4-validate): githubShortRefFor derives the "owner/repo#N" reference
// GitHub's closing-keyword parser actually recognizes, since a bare full URL
// is not a documented/recognized closing-keyword reference form.
func TestGithubShortRefFor(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"issue URL", "https://github.com/acme/widget/issues/42", "acme/widget#42"},
		{"PR URL", "https://github.com/acme/widget/pull/17", "acme/widget#17"},
		{"malformed URL returned unchanged", "https://example.com/foo", "https://example.com/foo"},
		{"strips query string", "https://github.com/acme/widget/issues/42?tab=comments", "acme/widget#42"},
		{"strips fragment", "https://github.com/acme/widget/issues/42#issuecomment-1", "acme/widget#42"},
		{"strips trailing slash", "https://github.com/acme/widget/issues/42/", "acme/widget#42"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := githubShortRefFor(tc.url)
			if got != tc.want {
				t.Errorf("githubShortRefFor(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

// TestBuildSessionInitialPrompt_RendersFixesInstructionForIssueURL proves AC3:
// the fact line renders inside the inert-data block (full URL, human-readable)
// and the instruction line renders after it using the short owner/repo#N
// reference, not the raw URL.
func TestBuildSessionInitialPrompt_RendersFixesInstructionForIssueURL(t *testing.T) {
	item := makeTestBacklogItem("Feature", "desc", `[{"index":0,"text":"do it","status":"pending"}]`, "ready", 1, "")
	item.ExternalURL = "https://github.com/acme/widget/issues/42"

	out := BuildSessionInitialPrompt(item, nil)

	factIdx := strings.Index(out, "Linked GitHub Issue/PR: https://github.com/acme/widget/issues/42")
	boundaryIdx := strings.Index(out, "--- END BACKLOG ITEM DATA ---")
	instructionIdx := strings.Index(out, "Fixes acme/widget#42")

	if factIdx == -1 {
		t.Fatalf("expected fact line in output:\n%s", out)
	}
	if instructionIdx == -1 {
		t.Fatalf("expected exact literal \"Fixes acme/widget#42\" in output:\n%s", out)
	}
	if factIdx > boundaryIdx {
		t.Errorf("fact line must appear before the inert-data boundary")
	}
	if instructionIdx < boundaryIdx {
		t.Errorf("instruction line must appear after the inert-data boundary")
	}
}

// TestBuildSessionInitialPrompt_RendersRelatedInstructionForPRURL proves the
// PR-shape branch renders the "Related:" keyword with the short reference.
func TestBuildSessionInitialPrompt_RendersRelatedInstructionForPRURL(t *testing.T) {
	item := makeTestBacklogItem("Feature", "desc", `[{"index":0,"text":"do it","status":"pending"}]`, "ready", 1, "")
	item.ExternalURL = "https://github.com/acme/widget/pull/17"

	out := BuildSessionInitialPrompt(item, nil)

	if !strings.Contains(out, "Related: acme/widget#17") {
		t.Errorf("expected exact literal \"Related: acme/widget#17\" in output:\n%s", out)
	}
}

// TestBuildSessionInitialPrompt_OmitsLinkedIssueSectionWhenExternalURLEmpty
// proves AC4: with no ExternalURL, neither the fact nor instruction line
// appears, and formatting is unchanged.
func TestBuildSessionInitialPrompt_OmitsLinkedIssueSectionWhenExternalURLEmpty(t *testing.T) {
	item := makeTestBacklogItem("Feature", "desc", `[{"index":0,"text":"do it","status":"pending"}]`, "ready", 1, "")
	item.ExternalURL = ""

	out := BuildSessionInitialPrompt(item, nil)

	unwanted := []string{"Linked GitHub Issue/PR", "Fixes ", "Related: "}
	for _, s := range unwanted {
		if strings.Contains(out, s) {
			t.Errorf("expected output to NOT contain %q when ExternalURL is empty\nOutput:\n%s", s, out)
		}
	}
}

// TestBuildTokenBudgetedPrompt_IncludesLinkedIssueSectionAfterTruncation
// proves AC5: both truncation passes still include the fact/instruction
// lines, since they only wrap BuildSessionInitialPrompt.
func TestBuildTokenBudgetedPrompt_IncludesLinkedIssueSectionAfterTruncation(t *testing.T) {
	longDesc := strings.Repeat("x", 17000)
	item := makeTestBacklogItem("Feature", longDesc, `[{"index":0,"text":"do it","status":"pending"}]`, "ready", 1, "")
	item.ExternalURL = "https://github.com/acme/widget/issues/42"

	out := BuildTokenBudgetedPrompt(item, nil)

	if !strings.Contains(out, "Linked GitHub Issue/PR: https://github.com/acme/widget/issues/42") {
		t.Errorf("expected fact line to survive truncation:\n%s", out)
	}
	if !strings.Contains(out, "Fixes acme/widget#42") {
		t.Errorf("expected instruction line to survive truncation:\n%s", out)
	}
}
