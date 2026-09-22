package server

import (
	"context"
	"fmt"
	"sync"
	"time"

	"jellyfin-organizer/internal/core"
	"jellyfin-organizer/internal/jellyfin"
)

const maxKeptRuns = 50

type Entry struct {
	Level   string `json:"level"` // info | change | warn | error
	Message string `json:"message"`
}

type Run struct {
	ID         string     `json:"id"`
	Source     string     `json:"source"` // manual | webhook
	Label      string     `json:"label"`
	DryRun     bool       `json:"dryRun"`
	Status     string     `json:"status"` // running | done | failed
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Changed    int        `json:"changed"`
	Errors     int        `json:"errors"`
	Entries    []Entry    `json:"entries,omitempty"`
}

type runStore struct {
	mu   sync.Mutex
	jf   *jellyfin.Client
	runs []*Run // newest first
	seq  int
}

func newRunStore(jf *jellyfin.Client) *runStore {
	return &runStore{jf: jf}
}

// Start launches the jobs in a background goroutine and returns the run ID.
func (s *runStore) Start(source, label string, jobs []core.Job, opt core.Options) *Run {
	s.mu.Lock()
	s.seq++
	run := &Run{
		ID:        fmt.Sprintf("run-%d-%d", time.Now().Unix(), s.seq),
		Source:    source,
		Label:     label,
		DryRun:    opt.DryRun,
		Status:    "running",
		StartedAt: time.Now(),
	}
	s.runs = append([]*Run{run}, s.runs...)
	if len(s.runs) > maxKeptRuns {
		s.runs = s.runs[:maxKeptRuns]
	}
	s.mu.Unlock()

	go s.execute(run, jobs, opt)
	return run
}

func (s *runStore) execute(run *Run, jobs []core.Job, opt core.Options) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	logf := func(level, message string) {
		s.mu.Lock()
		run.Entries = append(run.Entries, Entry{Level: level, Message: message})
		if level == "error" {
			run.Errors++
		}
		s.mu.Unlock()
	}

	failed := false
	for i, job := range jobs {
		name := job.SeriesName
		if name == "" {
			name = job.SeriesID
		}
		logf("info", fmt.Sprintf("— job %d/%d: %s (%s) —", i+1, len(jobs), name, job.Action))
		changed, err := core.RunJob(ctx, s.jf, job, opt, logf)
		s.mu.Lock()
		run.Changed += changed
		s.mu.Unlock()
		if err != nil {
			logf("error", fmt.Sprintf("job failed: %v", err))
			failed = true
			continue
		}
		verb := "changed"
		if opt.DryRun {
			verb = "would change"
		}
		logf("info", fmt.Sprintf("job done: %s %d episode(s)", verb, changed))
	}

	now := time.Now()
	s.mu.Lock()
	run.FinishedAt = &now
	if failed {
		run.Status = "failed"
	} else {
		run.Status = "done"
	}
	s.mu.Unlock()
}

// Get returns a deep copy of a run (including entries), or nil.
func (s *runStore) Get(id string) *Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runs {
		if r.ID == id {
			cp := *r
			cp.Entries = append([]Entry(nil), r.Entries...)
			return &cp
		}
	}
	return nil
}

// List returns run summaries (no entries), newest first.
func (s *runStore) List() []*Run {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Run, 0, len(s.runs))
	for _, r := range s.runs {
		cp := *r
		cp.Entries = nil
		out = append(out, &cp)
	}
	return out
}
