package session

// instance_actor.go provides the sendSyncErr / send helper layer that
// replaces stateMutex-held critical sections.  The current implementation
// is mutex-based (a thin wrapper over Instance.mu); a future refactor can
// replace the body with an actor-goroutine mailbox without changing any
// call site.

// instanceState is a capability token that proves the caller is executing
// inside a sendSyncErr or send critical section.  Code that requires write
// access to Instance fields takes *instanceState instead of *Instance so the
// compiler enforces the invariant at call sites.
type instanceState struct {
	inst *Instance
}

// sendSyncErr acquires the instance write lock, executes fn with a
// capability token, releases the lock, and returns the result.
// fn MUST NOT call sendSyncErr or Snapshot on the same Instance — doing so
// deadlocks because sync.RWMutex is not reentrant.
func (i *Instance) sendSyncErr(fn func(*instanceState) error) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	return fn(&instanceState{inst: i})
}

// send is the fire-and-forget variant of sendSyncErr.
func (i *Instance) send(fn func(*instanceState)) {
	i.mu.Lock()
	defer i.mu.Unlock()
	fn(&instanceState{inst: i})
}
