package session

import (
	"context"
	"sync"
)

// LiveInstance is the actor-owning handle for a session. It wraps *Instance with
// lifecycle fields for the future actor goroutine (Epic 3). Supported construction
// paths from outside this package:
//   - Registry.Acquire(sessionID) — load-or-construct for an existing persisted session
//   - Registry.Register(inst)     — for brand-new sessions in CreateSession (R2.18a)
//   - NewLiveInstance(inst)       — direct wrap when the caller already holds *Instance
//
// For Epic 2.5, the actor goroutine fields (ctx, cancel, done) are defined but inert;
// Epic 3's Task 3.1c extends newLiveInstance to also spawn the actor goroutine and
// gives stopActor() its real blocking body.
type LiveInstance struct {
	*Instance
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
	closeOnce sync.Once
}

// NewLiveInstance wraps an already-constructed *Instance in a LiveInstance.
// Use Registry.Acquire or Registry.Register where possible; call this directly
// only when the caller already holds a freshly-constructed *Instance (e.g.
// CreateSession, which builds its own via NewInstance and then passes it to
// Registry.Register).
func NewLiveInstance(inst *Instance) *LiveInstance {
	ctx, cancel := context.WithCancel(context.Background())
	return &LiveInstance{
		Instance: inst,
		ctx:      ctx,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
}

// newLiveInstance is the internal construction path used by Registry.Acquire.
// It calls FromInstanceData to reconstruct the *Instance from storage data
// (including Start() for Active sessions), then wraps it in a LiveInstance.
func newLiveInstance(data InstanceData, storage *Storage) (*LiveInstance, error) {
	inst, err := FromInstanceData(data)
	if err != nil {
		return nil, err
	}
	// Inject shell repository so shell operations can persist to the DB.
	if sr, ok := storage.repo.(ShellRepository); ok {
		inst.SetShellRepository(sr)
	}
	return NewLiveInstance(inst), nil
}

// Stop signals this instance's actor to exit and waits for it to drain.
// For Epic 2.5, no actor goroutine exists — this immediately closes done.
// Epic 3's Task 3.1c will replace this body with cancel()+<-done once the
// run loop exists to close done on exit.
func (l *LiveInstance) Stop() {
	l.stopActor()
}

// stopActor is the internal teardown called by Registry.release and ForceRelease.
// Safe to call multiple times (idempotent via sync.Once on the channel close).
func (l *LiveInstance) stopActor() {
	l.cancel()
	l.closeOnce.Do(func() { close(l.done) })
}
