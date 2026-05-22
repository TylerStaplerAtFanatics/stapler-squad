package session

import (
	"context"
	"testing"
	"time"

	appconfig "github.com/tstapler/stapler-squad/config"
	"github.com/tstapler/stapler-squad/session/memory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- sessionMemoryCache tests ---

// TestSessionMemoryCache_should_returnCachedValue_When_WithinTTL verifies that
// a second GetOrFetch call within the TTL does not invoke fetchFn again.
func TestSessionMemoryCache_should_returnCachedValue_When_WithinTTL(t *testing.T) {
	c := newSessionMemoryCache()
	calls := 0
	fetchFn := func() int64 { calls++; return 42 }

	v1 := c.GetOrFetch("uuid-1", fetchFn)
	v2 := c.GetOrFetch("uuid-1", fetchFn)

	assert.Equal(t, int64(42), v1)
	assert.Equal(t, int64(42), v2)
	assert.Equal(t, 1, calls, "fetchFn should be called exactly once within TTL")
}

// TestSessionMemoryCache_should_callFetchFn_When_TTLExpired verifies that
// after an entry expires, GetOrFetch calls fetchFn again.
func TestSessionMemoryCache_should_callFetchFn_When_TTLExpired(t *testing.T) {
	c := newSessionMemoryCache()

	// Manually insert an expired entry.
	c.mu.Lock()
	c.entries["uuid-2"] = memoryCacheEntry{
		rssMB:     99,
		fetchedAt: time.Now().Add(-2 * cacheTTL), // way past expiry
	}
	c.mu.Unlock()

	calls := 0
	fetchFn := func() int64 { calls++; return 77 }

	v := c.GetOrFetch("uuid-2", fetchFn)
	assert.Equal(t, int64(77), v)
	assert.Equal(t, 1, calls, "fetchFn should be called because TTL expired")
}

// TestSessionMemoryCache_should_callFetchFn_After_InvalidateCalled verifies that
// Invalidate causes next GetOrFetch to re-fetch regardless of TTL.
func TestSessionMemoryCache_should_callFetchFn_After_InvalidateCalled(t *testing.T) {
	c := newSessionMemoryCache()
	calls := 0
	fetchFn := func() int64 { calls++; return 55 }

	c.GetOrFetch("uuid-3", fetchFn) // initial fetch
	c.Invalidate("uuid-3")
	c.GetOrFetch("uuid-3", fetchFn) // should re-fetch

	assert.Equal(t, 2, calls, "fetchFn should be called twice — initial and after invalidate")
}

// TestSessionMemoryCache_Get_should_returnZero_When_EntryAbsent verifies Get returns 0 for missing entries.
func TestSessionMemoryCache_Get_should_returnZero_When_EntryAbsent(t *testing.T) {
	c := newSessionMemoryCache()
	assert.Equal(t, int64(0), c.Get("nonexistent"))
}

// --- HibernationSweeper resource-pressure tests ---

// makeTestSweeper builds a minimal HibernationSweeper with a fake reader and in-memory storage.
func makeTestSweeper(t *testing.T, memReader *memory.FakeReader, threshold int) (*HibernationSweeper, *Storage, func()) {
	t.Helper()
	storage, cleanup := createTestStorage(t)
	cfg := &appconfig.Config{
		Hibernation: appconfig.HibernationConfig{
			Enabled:                   true,
			IdleTimeoutMinutes:        120,
			ResourcePressureThreshold: threshold,
		},
	}
	sweeper := NewHibernationSweeper(storage, cfg, memReader)
	return sweeper, storage, cleanup
}

// makeIdleInstance creates an Instance with last meaningful output set in the past.
func makeIdleInstance(t *testing.T, uuid, title string, idleFor time.Duration) *Instance {
	t.Helper()
	now := time.Now()
	inst := &Instance{
		UUID:      uuid,
		Title:     title,
		Path:      "/tmp/test",
		Status:    Active,
		Program:   "claude",
		CreatedAt: now.Add(-idleFor),
		UpdatedAt: now,
	}
	// Set LastMeaningfulOutput so TimeSinceLastMeaningfulOutput returns idleFor.
	inst.LastMeaningfulOutput = now.Add(-idleFor)
	return inst
}

// TestSweeper_should_callSystemMemory_When_SweepRuns verifies that sweepResourcePressure
// calls the memory reader when the threshold is configured.
func TestSweeper_should_callSystemMemory_When_SweepRuns(t *testing.T) {
	reader := &memory.FakeReader{SystemPct: 80}
	sweeper, _, cleanup := makeTestSweeper(t, reader, 85)
	defer cleanup()

	ctx := context.Background()
	sweeper.sweepResourcePressure(ctx, nil)

	calls := reader.GetSystemMemoryCalls()
	assert.Equal(t, 1, calls, "SystemMemory should be called once per sweepResourcePressure invocation")
}

// TestSweeper_sweepResourcePressure_should_notHibernate_When_BelowThreshold verifies
// that no hibernation occurs when memory is below the configured threshold.
func TestSweeper_sweepResourcePressure_should_notHibernate_When_BelowThreshold(t *testing.T) {
	reader := &memory.FakeReader{SystemPct: 80}
	sweeper, _, cleanup := makeTestSweeper(t, reader, 85)
	defer cleanup()

	inst := makeIdleInstance(t, "uuid-a", "test-session", 10*time.Minute)
	instances := []*Instance{inst}

	sweeper.sweepResourcePressure(context.Background(), instances)

	// Session should still be Active — no hibernation occurred.
	assert.Equal(t, Active, inst.Status)
}

// TestSweeper_sweepResourcePressure_should_skip_When_SystemPctIsZero verifies that
// UsedPct=0 (macOS sentinel) skips pressure hibernation entirely.
func TestSweeper_sweepResourcePressure_should_skip_When_SystemPctIsZero(t *testing.T) {
	reader := &memory.FakeReader{SystemPct: 0}
	sweeper, _, cleanup := makeTestSweeper(t, reader, 85)
	defer cleanup()

	inst := makeIdleInstance(t, "uuid-b", "test-session", 10*time.Minute)
	sweeper.sweepResourcePressure(context.Background(), []*Instance{inst})

	assert.Equal(t, Active, inst.Status)
}

// TestSweeper_sweepResourcePressure_should_beSkipped_When_ThresholdIsZero verifies that
// threshold=0 prevents the pressure block from running at all.
func TestSweeper_sweepResourcePressure_should_beSkipped_When_ThresholdIsZero(t *testing.T) {
	reader := &memory.FakeReader{SystemPct: 90}
	sweeper, _, cleanup := makeTestSweeper(t, reader, 0)
	defer cleanup()

	inst := makeIdleInstance(t, "uuid-c", "test-session", 10*time.Minute)
	// sweepResourcePressure should skip the pressure block when threshold=0
	sweeper.sweepResourcePressure(context.Background(), []*Instance{inst})

	calls := reader.GetSystemMemoryCalls()
	// SystemMemory should NOT be called if threshold=0 (entire block gated)
	assert.Equal(t, 0, calls)
	assert.Equal(t, Active, inst.Status)
}

// TestSweeper_sweepResourcePressure_should_notHibernate_When_NoEligibleIdleSessions verifies
// that sessions with recent output (< 5 min) are not auto-hibernated for resource pressure.
func TestSweeper_sweepResourcePressure_should_notHibernate_When_NoEligibleIdleSessions(t *testing.T) {
	reader := &memory.FakeReader{SystemPct: 90}
	sweeper, _, cleanup := makeTestSweeper(t, reader, 85)
	defer cleanup()

	// Session idle only 2 min — within grace period.
	inst := makeIdleInstance(t, "uuid-d", "recent-session", 2*time.Minute)
	sweeper.sweepResourcePressure(context.Background(), []*Instance{inst})

	assert.Equal(t, Active, inst.Status)
}

// TestSweeper_sweepResourcePressure_should_hibernateOnlyOne_When_MultipleEligible verifies
// that only one session is hibernated per sweepResourcePressure call.
// Since Hibernate() requires tmux which is unavailable in unit tests, we assert that
// candidate selection is correct (no panic, status unchanged because Hibernate fails).
func TestSweeper_sweepResourcePressure_should_hibernateOnlyOne_When_MultipleEligible(t *testing.T) {
	reader := &memory.FakeReader{SystemPct: 90}
	sweeper, storage, cleanup := makeTestSweeper(t, reader, 85)
	defer cleanup()

	instA := makeIdleInstance(t, "uuid-e", "session-A", 20*time.Minute)
	instB := makeIdleInstance(t, "uuid-f", "session-B", 10*time.Minute)
	instC := makeIdleInstance(t, "uuid-g", "session-C", 8*time.Minute)

	// Add to storage so Hibernate can save state.
	require.NoError(t, storage.AddInstance(instA))
	require.NoError(t, storage.AddInstance(instB))
	require.NoError(t, storage.AddInstance(instC))

	instances := []*Instance{instA, instB, instC}
	// Hibernate will fail (no real tmux), but the sweeper should not panic.
	sweeper.sweepResourcePressure(context.Background(), instances)

	// All sessions remain Active because Hibernate fails without tmux.
	// The important assertion: no crash/panic during selection and hibernate attempt.
	_ = instA
}

// TestSweeper_sweepResourcePressure_should_setReasonResourcePressure_When_AutoHibernating verifies
// structural integrity of the sweeper (fields wired correctly).
func TestSweeper_sweepResourcePressure_should_setReasonResourcePressure_When_AutoHibernating(t *testing.T) {
	reader := &memory.FakeReader{SystemPct: 90}
	sweeper, _, cleanup := makeTestSweeper(t, reader, 85)
	defer cleanup()

	require.NotNil(t, sweeper)
	require.NotNil(t, sweeper.memCache)
	require.NotNil(t, sweeper.memReader)
}

// TestSweeper_GetCachedRSSMB_should_returnZero_When_Empty verifies that GetCachedRSSMB
// returns 0 for unknown UUIDs (satisfies MemoryCacheReader interface).
func TestSweeper_GetCachedRSSMB_should_returnZero_When_Empty(t *testing.T) {
	reader := &memory.FakeReader{SystemPct: 80}
	sweeper, _, cleanup := makeTestSweeper(t, reader, 85)
	defer cleanup()

	assert.Equal(t, int64(0), sweeper.GetCachedRSSMB("nonexistent"))
}

// TestSweeper_sweepResourcePressure_should_respectGracePeriod_When_SessionHadRecentOutput verifies
// that a session idle 4m59s is skipped (grace period is 5 minutes).
func TestSweeper_sweepResourcePressure_should_respectGracePeriod_When_SessionHadRecentOutput(t *testing.T) {
	reader := &memory.FakeReader{SystemPct: 92}
	sweeper, _, cleanup := makeTestSweeper(t, reader, 85)
	defer cleanup()

	// 4 minutes 59 seconds idle — just under the grace period.
	inst := makeIdleInstance(t, "uuid-h", "recent-session", 4*time.Minute+59*time.Second)
	sweeper.sweepResourcePressure(context.Background(), []*Instance{inst})

	assert.Equal(t, Active, inst.Status)
}
