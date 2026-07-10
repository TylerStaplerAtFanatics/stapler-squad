package session

import (
	"encoding/json"
	"errors"
)

// BacklogStatus represents the lifecycle state of a backlog item.
type BacklogStatus string

const (
	BacklogStatusIdea       BacklogStatus = "idea"
	BacklogStatusRefining   BacklogStatus = "refining"
	BacklogStatusReady      BacklogStatus = "ready"
	BacklogStatusInProgress BacklogStatus = "in_progress"
	BacklogStatusReview     BacklogStatus = "review"
	BacklogStatusPRPending  BacklogStatus = "pr_pending"
	BacklogStatusDone       BacklogStatus = "done"
	BacklogStatusArchived   BacklogStatus = "archived"
)

// Session role constants.
const (
	SessionRoleWork   = "work"
	SessionRoleTriage = "triage"
	SessionRoleReview = "review"
)

// Session tag constants for backlog-spawned sessions.
const (
	TagBacklogWork     = "backlog:work"
	TagBacklogRevision = "backlog:revision"
	TagAutonomous      = "autonomous"
)

// TriggeredBy values for BacklogStatusEvent records.
const (
	TriggeredByUser   = "user"
	TriggeredBySystem = "system"
)

// DefaultBacklogPriority is the default priority assigned to new backlog items
// when no priority is specified. Lower values indicate higher priority.
const DefaultBacklogPriority = 3

// AcStatus represents the status of a single acceptance criterion.
type AcStatus string

const (
	AcStatusPending    AcStatus = "pending"
	AcStatusInProgress AcStatus = "in_progress"
	AcStatusDone       AcStatus = "done"
	AcStatusFail       AcStatus = "fail"
)

// IsValid reports whether s is a known AC status value.
func (s AcStatus) IsValid() bool {
	switch s {
	case AcStatusPending, AcStatusInProgress, AcStatusDone, AcStatusFail:
		return true
	}
	return false
}

// AcCriteriaJSON is the JSON-serialized form of []AcCriterion stored in the DB.
// Using a named type prevents silently passing Description or other string fields
// where serialized AC criteria are expected.
type AcCriteriaJSON string

// AcCriteriaJSONEmpty is the zero value — an empty criteria list.
const AcCriteriaJSONEmpty AcCriteriaJSON = ""

// Parse deserializes the criteria from JSON.
func (j AcCriteriaJSON) Parse() ([]AcCriterion, error) {
	return ParseAcCriteria(j)
}

// IsEmpty reports whether j contains no criteria JSON.
func (j AcCriteriaJSON) IsEmpty() bool { return j == "" }

// AcCriterion is a single acceptance criterion for a backlog item.
type AcCriterion struct {
	Index  int      `json:"index"`
	Text   string   `json:"text"`
	Status AcStatus `json:"status"` // pending, in_progress, done, fail
	Note   string   `json:"note,omitempty"`
}

// ParseAcCriteria deserializes acceptance criteria from a JSON string.
func ParseAcCriteria(raw AcCriteriaJSON) ([]AcCriterion, error) {
	if raw == "" {
		return nil, nil
	}
	var criteria []AcCriterion
	if err := json.Unmarshal([]byte(raw), &criteria); err != nil {
		return nil, err
	}
	return criteria, nil
}

// SerializeAcCriteria serializes acceptance criteria to an AcCriteriaJSON value.
func SerializeAcCriteria(criteria []AcCriterion) (AcCriteriaJSON, error) {
	b, err := json.Marshal(criteria)
	if err != nil {
		return "", err
	}
	return AcCriteriaJSON(b), nil
}

// ReviewOutcome is a typed verdict outcome value (PASS, FAIL, PARTIAL, UNVERIFIABLE).
type ReviewOutcome string

const (
	ReviewOutcomePass         ReviewOutcome = "PASS"
	ReviewOutcomeFail         ReviewOutcome = "FAIL"
	ReviewOutcomePartial      ReviewOutcome = "PARTIAL"
	ReviewOutcomeUnverifiable ReviewOutcome = "UNVERIFIABLE"
)

// IsValid reports whether o is a recognised review outcome.
func (o ReviewOutcome) IsValid() bool {
	switch o {
	case ReviewOutcomePass, ReviewOutcomeFail, ReviewOutcomePartial, ReviewOutcomeUnverifiable:
		return true
	}
	return false
}

// Backward-compatible aliases so callers can be migrated incrementally.
// Prefer ReviewOutcome* constants in new code.
const (
	ReviewVerdictPass         = ReviewOutcomePass
	ReviewVerdictFail         = ReviewOutcomeFail
	ReviewVerdictPartial      = ReviewOutcomePartial
	ReviewVerdictUnverifiable = ReviewOutcomeUnverifiable
)

// CriterionVerdict holds the review outcome for a single acceptance criterion.
type CriterionVerdict struct {
	CriterionIndex int           `json:"criterion_index"`
	Outcome        ReviewOutcome `json:"outcome"`
	Evidence       string        `json:"evidence"`
}

// AggregateOutcome computes the overall outcome from a slice of CriterionVerdicts.
// Priority (highest to lowest): FAIL > PARTIAL > UNVERIFIABLE > PASS.
// Returns FAIL when the slice is empty to prevent auto-approval of empty reviews.
func AggregateOutcome(verdicts []CriterionVerdict) ReviewOutcome {
	if len(verdicts) == 0 {
		// No criteria evaluated — treat as FAIL, not PASS, to prevent auto-approval
		// of reviews that somehow bypassed the non-empty validation in submit_review_verdict.
		return ReviewOutcomeFail
	}

	hasFail := false
	hasPartial := false
	hasUnverifiable := false

	for _, v := range verdicts {
		switch v.Outcome {
		case ReviewOutcomeFail:
			hasFail = true
		case ReviewOutcomePartial:
			hasPartial = true
		case ReviewOutcomeUnverifiable:
			hasUnverifiable = true
		}
	}

	switch {
	case hasFail:
		return ReviewOutcomeFail
	case hasPartial:
		return ReviewOutcomePartial
	case hasUnverifiable:
		return ReviewOutcomeUnverifiable
	default:
		return ReviewOutcomePass
	}
}

// validTransitions is the authoritative state machine transition table.
// idea→ready is a fast-track for items that already have AC written; items
// needing AC work should go idea→refining→ready instead.
var validTransitions = map[BacklogStatus]map[BacklogStatus]bool{
	BacklogStatusIdea: {
		BacklogStatusReady:    true,
		BacklogStatusRefining: true,
		BacklogStatusArchived: true,
	},
	BacklogStatusRefining: {
		BacklogStatusIdea:     true, // backward: re-triage
		BacklogStatusReady:    true,
		BacklogStatusArchived: true,
	},
	BacklogStatusReady: {
		BacklogStatusInProgress: true,
		BacklogStatusIdea:       true, // backward: re-triage
		BacklogStatusRefining:   true, // backward: refine ACs
		BacklogStatusArchived:   true,
	},
	BacklogStatusInProgress: {
		BacklogStatusReview:   true,
		BacklogStatusReady:    true,
		BacklogStatusRefining: true, // backward: refine ACs/plan
		BacklogStatusIdea:     true, // backward: re-triage from scratch
	},
	BacklogStatusReview: {
		BacklogStatusPRPending:  true,
		BacklogStatusDone:       true,
		BacklogStatusInProgress: true,
		BacklogStatusReady:      true, // backward: re-spawn without re-triaging
		BacklogStatusRefining:   true, // backward: refine ACs/plan
		BacklogStatusIdea:       true, // backward: re-triage from scratch
	},
	BacklogStatusPRPending: {
		BacklogStatusDone:       true,
		BacklogStatusInProgress: true,
		BacklogStatusReview:     true,
		BacklogStatusReady:      true, // backward
		BacklogStatusRefining:   true, // backward
		BacklogStatusIdea:       true, // backward
	},
	BacklogStatusDone: {
		BacklogStatusReview:     true,
		BacklogStatusArchived:   true,
		BacklogStatusInProgress: true, // backward
		BacklogStatusReady:      true, // backward
		BacklogStatusRefining:   true, // backward
		BacklogStatusIdea:       true, // backward
	},
	BacklogStatusArchived: {
		BacklogStatusIdea: true,
	},
}

// CanTransitionBacklog reports whether a transition from one backlog status to another is permitted.
func CanTransitionBacklog(from, to BacklogStatus) bool {
	targets, ok := validTransitions[from]
	if !ok {
		return false
	}
	return targets[to]
}

// Sentinel errors for transition guards.
var (
	ErrACRequired            = errors.New("acceptance criteria required before marking ready")
	ErrPlanRequired          = errors.New("plan must be approved or skip_planning must be true before spawning work session")
	ErrPlanArtifactsRequired = errors.New("plan artifacts path is required when planning is not skipped")
	ErrVerdictRequired       = errors.New("PASS verdict or manual override required before marking done")
)

// BacklogItemTransitionInput carries the fields needed by TransitionGuard.
type BacklogItemTransitionInput struct {
	Status            BacklogStatus
	AcCriteria        AcCriteriaJSON // serialized acceptance criteria
	PlanApproved      bool
	SkipPlanning      bool
	PlanArtifactsPath string        // path to plan artifacts written by triage session
	OverallOutcome    ReviewOutcome // from linked ReviewVerdict
	OverrideReason    string
}

// TransitionGuard validates business rules before a status transition.
// It returns nil when the transition is allowed, or a sentinel error when a
// guard condition is violated. It does NOT check CanTransition — callers must
// invoke CanTransition separately if structural validity is also required.
func TransitionGuard(item BacklogItemTransitionInput, to BacklogStatus) error {
	from := item.Status

	switch {
	case from == BacklogStatusIdea && to == BacklogStatusReady:
		criteria, err := ParseAcCriteria(item.AcCriteria)
		if err != nil || len(criteria) == 0 {
			return ErrACRequired
		}
		return nil

	case from == BacklogStatusReady && to == BacklogStatusInProgress:
		if !item.PlanApproved && !item.SkipPlanning {
			return ErrPlanRequired
		}
		if item.PlanApproved && !item.SkipPlanning && item.PlanArtifactsPath == "" {
			return ErrPlanArtifactsRequired
		}
		return nil

	case from == BacklogStatusRefining && to == BacklogStatusReady:
		criteria, err := ParseAcCriteria(item.AcCriteria)
		if err != nil || len(criteria) == 0 {
			return ErrACRequired
		}
		return nil

	case from == BacklogStatusReview && to == BacklogStatusDone:
		if item.OverrideReason != "" {
			return nil
		}
		if item.OverallOutcome != ReviewOutcomePass {
			return ErrVerdictRequired
		}
		return nil

	default:
		// All other permitted transitions have no additional guards.
		return nil
	}
}
