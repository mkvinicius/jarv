// Package scheduler provides cron-like task scheduling for JARV.
//
// Tasks can be scheduled with simple interval expressions:
//   - "every 10m"   — every 10 minutes
//   - "every 2h"    — every 2 hours
//   - "every 1d"    — every day
//   - "daily 09:00" — every day at 9am
//
// Tasks are persisted in the config file and survive restarts.
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
package scheduler

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ─────────────────────────────────────────────────────────────────────────────
// Task
// ─────────────────────────────────────────────────────────────────────────────

// Task is a scheduled agent prompt.
type Task struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Schedule string    `json:"schedule"` // e.g. "every 1h", "daily 09:00"
	Prompt   string    `json:"prompt"`
	Enabled  bool      `json:"enabled"`
	NextRun  time.Time `json:"next_run"`
	LastRun  time.Time `json:"last_run,omitempty"`
}

// Interval returns the duration between runs.
func (t *Task) Interval() (time.Duration, error) {
	return parseSchedule(t.Schedule)
}

// ─────────────────────────────────────────────────────────────────────────────
// Scheduler
// ─────────────────────────────────────────────────────────────────────────────

// Handler is called when a task fires. Receives the task and should return an error if execution fails.
type Handler func(ctx context.Context, task Task) error

// Scheduler runs tasks on their configured schedules.
type Scheduler struct {
	mu      sync.RWMutex
	tasks   []Task
	onChange func([]Task) // called when tasks change (for persistence)
}

// New creates a new Scheduler.
func New(onChange func([]Task)) *Scheduler {
	return &Scheduler{onChange: onChange}
}

// SetTasks replaces all tasks (used when loading from config).
func (s *Scheduler) SetTasks(tasks []Task) {
	s.mu.Lock()
	s.tasks = tasks
	s.mu.Unlock()
}

// Add adds a new scheduled task.
func (s *Scheduler) Add(name, schedule, prompt string) (Task, error) {
	if _, err := parseSchedule(schedule); err != nil {
		return Task{}, fmt.Errorf("invalid schedule %q: %w", schedule, err)
	}

	s.mu.Lock()
	task := Task{
		ID:       uuid.NewString()[:8],
		Name:     name,
		Schedule: schedule,
		Prompt:   prompt,
		Enabled:  true,
		NextRun:  time.Now(),
	}
	s.tasks = append(s.tasks, task)
	tasks := make([]Task, len(s.tasks))
	copy(tasks, s.tasks)
	s.mu.Unlock()

	if s.onChange != nil {
		s.onChange(tasks)
	}
	return task, nil
}

// Remove removes a task by ID.
func (s *Scheduler) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.tasks {
		if t.ID == id {
			s.tasks = append(s.tasks[:i], s.tasks[i+1:]...)
			if s.onChange != nil {
				tasks := make([]Task, len(s.tasks))
				copy(tasks, s.tasks)
				s.onChange(tasks)
			}
			return nil
		}
	}
	return fmt.Errorf("task %q not found", id)
}

// List returns a copy of all tasks.
func (s *Scheduler) List() []Task {
	s.mu.RLock()
	out := make([]Task, len(s.tasks))
	copy(out, s.tasks)
	s.mu.RUnlock()
	return out
}

// Disable disables a task by ID without removing it.
func (s *Scheduler) Disable(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.tasks {
		if t.ID == id {
			s.tasks[i].Enabled = false
			if s.onChange != nil {
				tasks := make([]Task, len(s.tasks))
				copy(tasks, s.tasks)
				s.onChange(tasks)
			}
			return nil
		}
	}
	return fmt.Errorf("task %q not found", id)
}

// Start begins the scheduler loop. Blocks until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context, handler Handler) {
	ticker := time.NewTicker(30 * time.Second) // check every 30s
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx, handler)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context, handler Handler) {
	now := time.Now()
	s.mu.Lock()
	var due []Task
	for i, t := range s.tasks {
		if !t.Enabled {
			continue
		}
		if now.After(t.NextRun) || now.Equal(t.NextRun) {
			due = append(due, t)
			interval, err := parseSchedule(t.Schedule)
			if err == nil {
				s.tasks[i].NextRun = now.Add(interval)
				s.tasks[i].LastRun = now
			}
		}
	}
	tasks := make([]Task, len(s.tasks))
	copy(tasks, s.tasks)
	s.mu.Unlock()

	if len(due) > 0 && s.onChange != nil {
		s.onChange(tasks)
	}

	for _, t := range due {
		go func(task Task) {
			taskCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			if err := handler(taskCtx, task); err != nil {
				fmt.Printf("[cron] task %q error: %v\n", task.Name, err)
			}
		}(t)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Schedule parsing
// ─────────────────────────────────────────────────────────────────────────────

// parseSchedule converts schedule strings to durations.
//
// Supported formats:
//   - "every 30s"   → 30 seconds
//   - "every 10m"   → 10 minutes
//   - "every 2h"    → 2 hours
//   - "every 1d"    → 24 hours
//   - "daily 09:00" → calculated to next 09:00
func parseSchedule(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))

	if strings.HasPrefix(s, "every ") {
		part := strings.TrimPrefix(s, "every ")
		part = strings.TrimSpace(part)

		if len(part) < 2 {
			return 0, fmt.Errorf("invalid schedule: %q", s)
		}

		unit := part[len(part)-1]
		numStr := part[:len(part)-1]
		n, err := strconv.Atoi(numStr)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid number in schedule: %q", s)
		}

		switch unit {
		case 's':
			return time.Duration(n) * time.Second, nil
		case 'm':
			return time.Duration(n) * time.Minute, nil
		case 'h':
			return time.Duration(n) * time.Hour, nil
		case 'd':
			return time.Duration(n) * 24 * time.Hour, nil
		default:
			return 0, fmt.Errorf("unknown unit %q in schedule %q", string(unit), s)
		}
	}

	if strings.HasPrefix(s, "daily ") {
		// Return 24h as the interval; NextRun calculation handled separately
		return 24 * time.Hour, nil
	}

	return 0, fmt.Errorf("unsupported schedule format: %q (use 'every Ns/m/h/d' or 'daily HH:MM')", s)
}

// NextRunAfter calculates the next run time for a schedule starting from now.
func NextRunAfter(schedule string, from time.Time) time.Time {
	s := strings.TrimSpace(strings.ToLower(schedule))
	if strings.HasPrefix(s, "daily ") {
		timePart := strings.TrimPrefix(s, "daily ")
		parts := strings.SplitN(timePart, ":", 2)
		if len(parts) == 2 {
			h, _ := strconv.Atoi(parts[0])
			min, _ := strconv.Atoi(parts[1])
			next := time.Date(from.Year(), from.Month(), from.Day(), h, min, 0, 0, from.Location())
			if !next.After(from) {
				next = next.Add(24 * time.Hour)
			}
			return next
		}
	}
	d, err := parseSchedule(schedule)
	if err != nil {
		return from.Add(time.Hour)
	}
	return from.Add(d)
}
