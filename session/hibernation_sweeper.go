package session

import (
	"context"
	"time"

	appconfig "github.com/tstapler/stapler-squad/config"
	"github.com/tstapler/stapler-squad/log"
)

// LiveInstancesProvider is satisfied by ReviewQueuePoller. It returns the live
// in-memory instances without constructing new Instance objects or spawning PTY
// processes. HibernationSweeper uses this as a fast path to avoid LoadInstances().
type LiveInstancesProvider interface {
	GetInstances() []*Instance
}

// HibernationSweeper periodically checks all sessions and hibernates those
// that have been idle longer than the configured timeout.
// It also prunes stale checkpoint data older than the retention period.
type HibernationSweeper struct {
	storage      *Storage
	cfg          *appconfig.Config
	liveProvider LiveInstancesProvider
}

// NewHibernationSweeper creates a HibernationSweeper using the given storage and config.
func NewHibernationSweeper(storage *Storage, cfg *appconfig.Config) *HibernationSweeper {
	return &HibernationSweeper{
		storage: storage,
		cfg:     cfg,
	}
}

// SetLiveProvider wires the fast-path instance source. Call this after constructing
// the ReviewQueuePoller so that sweep() uses live in-memory instances instead of
// calling LoadInstances() (which spawns PTY/tmux subprocesses).
func (s *HibernationSweeper) SetLiveProvider(p LiveInstancesProvider) {
	s.liveProvider = p
}

// Start runs the periodic sweep loop. Blocks until ctx is cancelled.
func (s *HibernationSweeper) Start(ctx context.Context) {
	interval := 5 * time.Minute
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Info("hibernation sweeper started",
		"idle_timeout_minutes", s.cfg.Hibernation.IdleTimeoutMinutes,
		"check_interval", interval)

	for {
		select {
		case <-ctx.Done():
			log.Info("hibernation sweeper stopped")
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

// sweep examines all Active sessions and hibernates those that have exceeded the
// idle timeout.
func (s *HibernationSweeper) sweep(ctx context.Context) {
	if !s.cfg.Hibernation.Enabled {
		return
	}

	idleTimeout := time.Duration(s.cfg.Hibernation.IdleTimeoutMinutes) * time.Minute
	if idleTimeout <= 0 {
		return
	}

	var instances []*Instance
	if s.liveProvider != nil {
		instances = s.liveProvider.GetInstances()
	} else {
		var err error
		instances, err = s.storage.LoadInstances()
		if err != nil {
			log.Error("hibernation sweeper: failed to load instances", "err", err)
			return
		}
	}

	for _, inst := range instances {
		if !inst.IsActive() {
			continue
		}
		idle := inst.TimeSinceLastMeaningfulOutput(inst.CreatedAt)
		if idle >= idleTimeout {
			log.Info("auto-hibernating idle session",
				"session", inst.Title,
				"idle_duration", idle.Round(time.Minute))
			inst.SetHibernateReason("idle")
			if err := inst.Hibernate(ctx); err != nil {
				log.Warn("auto-hibernate failed", "session", inst.Title, "err", err)
				continue
			}
			if err := s.storage.SaveInstances(instances); err != nil {
				log.Warn("auto-hibernate: failed to save instance state",
					"session", inst.Title, "err", err)
			}
		}
	}
}
