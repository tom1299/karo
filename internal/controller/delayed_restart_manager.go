/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
)

var (
	// ErrNegativeDelay is returned when a negative delay is provided
	ErrNegativeDelay = errors.New("delay cannot be negative")
	// ErrNilRestartFunc is returned when a nil restart function is provided
	ErrNilRestartFunc = errors.New("restartFunc cannot be nil")
)

// TargetKey uniquely identifies a restart target (deployment/statefulset)
type TargetKey struct {
	Kind      string
	Name      string
	Namespace string
}

// String returns a string representation of the TargetKey for logging
func (t TargetKey) String() string {
	return fmt.Sprintf("%s/%s/%s", t.Kind, t.Namespace, t.Name)
}

// RestartFunc is a function that performs the actual restart operation
type RestartFunc func(ctx context.Context) error

// DelayedRestartManager manages delayed restart operations for workloads
type DelayedRestartManager interface {
	// ScheduleRestart schedules a delayed restart for a target
	// Returns the final delay being used (in case it's already scheduled with a different delay)
	// and whether this is a new schedule (false if already scheduled)
	ScheduleRestart(ctx context.Context, target TargetKey, delay time.Duration, restartFunc RestartFunc) (finalDelay time.Duration, isNew bool, err error)

	// IsScheduled checks if a target already has a pending delayed restart
	IsScheduled(target TargetKey) bool

	// Cancel cancels a scheduled restart for a target
	// TODO: Could return bool if there was something to cancel
	Cancel(target TargetKey)

	// Shutdown gracefully shuts down the manager, canceling all pending restarts
	Shutdown()
}

// delayedTask represents a scheduled restart task
type delayedTask struct {
	target      TargetKey
	delay       time.Duration
	restartFunc RestartFunc
	timer       *time.Timer
	cancelCtx   context.Context
	cancelFunc  context.CancelFunc
	scheduledAt time.Time
	executeAt   time.Time
}

// delayedRestartManager is the concrete implementation of DelayedRestartManager
type delayedRestartManager struct {
	mu           sync.RWMutex
	tasks        map[TargetKey]*delayedTask
	shutdownOnce sync.Once
	logger       logr.Logger
}

// NewDelayedRestartManager creates a new DelayedRestartManager instance
//
//nolint:ireturn // Factory function returning interface is intentional
func NewDelayedRestartManager(logger logr.Logger) DelayedRestartManager {
	return &delayedRestartManager{
		tasks:  make(map[TargetKey]*delayedTask),
		logger: logger,
	}
}

// ScheduleRestart schedules a delayed restart for a target
func (m *delayedRestartManager) ScheduleRestart(ctx context.Context, target TargetKey, delay time.Duration, restartFunc RestartFunc) (finalDelay time.Duration, isNew bool, err error) {
	if delay < 0 {
		return 0, false, fmt.Errorf("%w: %v", ErrNegativeDelay, delay)
	}

	if restartFunc == nil {
		return 0, false, ErrNilRestartFunc
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if there's already a scheduled restart for this target
	existingTask, exists := m.tasks[target]
	if exists {

		m.logger.Info("Target already scheduled, keeping existing schedule",
			"target", target.String(),
			"existingDelay", existingTask.delay,
			"requestedDelay", delay,
			"willExecuteAt", existingTask.executeAt.Format(time.RFC3339))

		return existingTask.delay, false, nil
	}

	// No existing task, schedule a new one
	return m.scheduleNewTask(ctx, target, delay, restartFunc)
}

// IsScheduled checks if a target already has a pending delayed restart
func (m *delayedRestartManager) IsScheduled(target TargetKey) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, exists := m.tasks[target]

	return exists
}

// Cancel cancels a scheduled restart for a target
func (m *delayedRestartManager) Cancel(target TargetKey) {
	m.mu.Lock()
	defer m.mu.Unlock()

	task, exists := m.tasks[target]
	if !exists {
		m.logger.Info("No scheduled restart to cancel", "target", target.String())

		return
	}

	m.logger.Info("Canceling scheduled restart",
		"target", target.String(),
		"delay", task.delay,
		"wasScheduledAt", task.scheduledAt.Format(time.RFC3339))

	// Cancel the task
	task.cancelFunc()
	if task.timer != nil {
		task.timer.Stop()
	}

	// Remove from map
	delete(m.tasks, target)
}

// Shutdown gracefully shuts down the manager, canceling all pending restarts
func (m *delayedRestartManager) Shutdown() {
	m.shutdownOnce.Do(func() {
		m.mu.Lock()
		defer m.mu.Unlock()

		m.logger.Info("Shutting down DelayedRestartManager",
			"pendingTasks", len(m.tasks))

		// Cancel all pending tasks
		for target, task := range m.tasks {
			m.logger.Info("Canceling pending restart due to shutdown",
				"target", target.String(),
				"delay", task.delay)

			task.cancelFunc()
			if task.timer != nil {
				task.timer.Stop()
			}
		}

		// Clear the tasks map
		m.tasks = make(map[TargetKey]*delayedTask)

		m.logger.Info("DelayedRestartManager shutdown complete")
	})
}

// GetScheduledRestarts returns information about all currently scheduled restarts
// This is useful for debugging and testing
func (m *delayedRestartManager) GetScheduledRestarts() map[TargetKey]time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[TargetKey]time.Time, len(m.tasks))
	for target, task := range m.tasks {
		result[target] = task.executeAt
	}

	return result
}

// scheduleNewTask creates and schedules a new delayed restart task
// Must be called with m.mu locked
//
//nolint:unparam // error return kept for consistency with ScheduleRestart signature
func (m *delayedRestartManager) scheduleNewTask(_ context.Context, target TargetKey, delay time.Duration, restartFunc RestartFunc) (time.Duration, bool, error) {
	// Create a new context for this task that can be canceled independently
	taskCtx, cancelFunc := context.WithCancel(context.Background())

	executeAt := time.Now().Add(delay)

	task := &delayedTask{
		target:      target,
		delay:       delay,
		restartFunc: restartFunc,
		cancelCtx:   taskCtx,
		cancelFunc:  cancelFunc,
		scheduledAt: time.Now(),
		executeAt:   executeAt,
	}

	// Create timer for delayed execution
	task.timer = time.AfterFunc(delay, func() {
		m.executeTask(task)
	})

	// Store the task
	m.tasks[target] = task

	m.logger.Info("Scheduled delayed restart",
		"target", target.String(),
		"delay", delay,
		"executeAt", executeAt.Format(time.RFC3339))

	return delay, true, nil
}

// executeTask executes a delayed restart task
func (m *delayedRestartManager) executeTask(task *delayedTask) {
	logger := m.logger.WithValues("target", task.target.String())

	// Check if task was canceled
	select {
	case <-task.cancelCtx.Done():
		logger.Info("Restart task was canceled before execution")

		return
	default:
	}

	logger.Info("Executing delayed restart",
		"delay", task.delay,
		"scheduledAt", task.scheduledAt.Format(time.RFC3339))

	// Execute the restart function
	if err := task.restartFunc(task.cancelCtx); err != nil {
		logger.Error(err, "Failed to execute delayed restart")
	} else {
		logger.Info("Successfully executed delayed restart")
	}

	// Remove task from map after execution
	m.mu.Lock()
	delete(m.tasks, task.target)
	m.mu.Unlock()
}
