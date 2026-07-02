package session

// instance_snapshot.go defines InstanceSnapshot — a point-in-time copy of
// Instance's mutable fields — and the Snapshot() accessor.
//
// Snapshot() acquires mu.RLock() and copies the fields callers need for
// read-only access.  All mutation still happens via sendSyncErr / send
// (mu.Lock).  The snapshot is intentionally a value type (returned as *
// to avoid stack-allocating large structs on every read).

import (
	"time"

	"github.com/tstapler/stapler-squad/session/artifacts"
)

// InstanceSnapshot is a read-only view of an Instance's mutable data at a
// single point in time.  It is created by Snapshot() and is safe to use
// from any goroutine without additional locking.
//
// The field set must be updated whenever a mutable field is added to
// Instance — this is the single authoritative place that tracks them.
type InstanceSnapshot struct {
	// Lifecycle
	Status  Status
	Started bool

	// Review timestamps (promoted from ReviewState)
	LastAcknowledged      time.Time
	LastAddedToQueue      time.Time
	LastTerminalUpdate    time.Time
	LastMeaningfulOutput  time.Time
	LastOutputSignature   string
	LastViewed            time.Time
	LastPromptDetected    time.Time
	LastPromptSignature   string
	LastUserResponse      time.Time
	ProcessingGraceUntil  time.Time

	// Creation time (immutable after construction, safe to read from Instance
	// directly — included here so callers don't need two sources).
	CreatedAt time.Time

	// Tags (defensive copy — callers may read without lock)
	Tags []string

	// Checkpoints (defensive copy)
	Checkpoints CheckpointList

	// GitHub PR number (for HasGitHubPR)
	GitHubPRNumber int

	// GitHub PR status fields
	GitHubPRURL            string
	GitHubPRState          string
	GitHubPRPriority       string
	GitHubPRIsDraft        bool
	GitHubApprovedCount    int
	GitHubChangesReqCount  int
	GitHubCheckConclusion  string
	GitHubPRStatusTerminal bool
	GitHubIsFork           bool
	LastPRStatusCheck      time.Time
	GitHubOwner            string
	GitHubRepo             string
	Branch                 string

	// ArchivedAt (pointer — nil if not archived)
	ArchivedAt *time.Time

	// Artifacts (pointer — nil until populated)
	Artifacts *artifacts.SessionArtifactsBlob
}

// Snapshot builds a point-in-time copy of the Instance's mutable fields.
// Acquires mu.RLock; the snapshot is then safe to use without any lock.
func (i *Instance) Snapshot() *InstanceSnapshot {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.buildSnapshot()
}

// buildSnapshot builds the snapshot while mu is already held.
// Callers must hold i.mu (read or write) before calling this.
func (i *Instance) buildSnapshot() *InstanceSnapshot {
	snap := &InstanceSnapshot{
		Status:  i.Status,
		Started: i.started,

		LastAcknowledged:     i.LastAcknowledged,
		LastAddedToQueue:     i.LastAddedToQueue,
		LastTerminalUpdate:   i.LastTerminalUpdate,
		LastMeaningfulOutput: i.LastMeaningfulOutput,
		LastOutputSignature:  i.LastOutputSignature,
		LastViewed:           i.LastViewed,
		LastPromptDetected:   i.LastPromptDetected,
		LastPromptSignature:  i.LastPromptSignature,
		LastUserResponse:     i.LastUserResponse,
		ProcessingGraceUntil: i.ProcessingGraceUntil,

		CreatedAt: i.CreatedAt,

		GitHubPRNumber:         i.GitHubPRNumber,
		GitHubPRURL:            i.GitHubPRURL,
		GitHubPRState:          i.GitHubPRState,
		GitHubPRPriority:       i.GitHubPRPriority,
		GitHubPRIsDraft:        i.GitHubPRIsDraft,
		GitHubApprovedCount:    i.GitHubApprovedCount,
		GitHubChangesReqCount:  i.GitHubChangesReqCount,
		GitHubCheckConclusion:  i.GitHubCheckConclusion,
		GitHubPRStatusTerminal: i.GitHubPRStatusTerminal,
		GitHubIsFork:           i.GitHubIsFork,
		LastPRStatusCheck:      i.LastPRStatusCheck,
		GitHubOwner:            i.GitHubOwner,
		GitHubRepo:             i.GitHubRepo,
		Branch:                 i.Branch,

		Artifacts: i.Artifacts,
	}

	// Deep-copy slice fields so callers cannot mutate the Instance's backing arrays.
	if i.Tags != nil {
		snap.Tags = make([]string, len(i.Tags))
		copy(snap.Tags, i.Tags)
	}
	if i.Checkpoints != nil {
		snap.Checkpoints = make(CheckpointList, len(i.Checkpoints))
		copy(snap.Checkpoints, i.Checkpoints)
	}
	if i.ArchivedAt != nil {
		t := *i.ArchivedAt
		snap.ArchivedAt = &t
	}

	return snap
}
