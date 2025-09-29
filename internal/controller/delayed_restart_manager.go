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

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// DelayedRestartManager manages delayed restart operations for targets
type DelayedRestartManager interface {
	// ScheduleRestart schedules a restart for the target after calculating the appropriate delay
	ScheduleRestart(ctx context.Context, targetKey string, rules []*karov1alpha1.RestartRule, restartFunc func() error)

	// IsRestartPending checks if a restart is currently pending for the given target
	IsRestartPending(targetKey string) bool

	// Stop gracefully shuts down all pending operations
	Stop()
}

// restartTask represents a pending restart operation
type restartTask struct {
	targetKey   string
	delay       time.Duration
	restartFunc func() error
	cancelFunc  context.CancelFunc
	done        chan struct{}
}

var (
	// ErrTaskNotFound is returned when no pending task is found for a target
	ErrTaskNotFound = errors.New("no pending task found for target")

	// ErrTaskTimeout is returned when a task times out
	ErrTaskTimeout = errors.New("timeout waiting for task to complete")
)

// DelayedRestartManagerImpl is the concrete implementation of DelayedRestartManager
type DelayedRestartManagerImpl struct {
	mu           sync.RWMutex
	pendingTasks map[string]*restartTask
	stopCh       chan struct{}
	wg           sync.WaitGroup
	stopped      bool
}

// NewDelayedRestartManager creates a new DelayedRestartManager
//
//nolint:ireturn
func NewDelayedRestartManager() DelayedRestartManager {
	return &DelayedRestartManagerImpl{
		pendingTasks: make(map[string]*restartTask),
		stopCh:       make(chan struct{}),
	}
}

// ScheduleRestart schedules a restart for the target after calculating the appropriate delay
func (m *DelayedRestartManagerImpl) ScheduleRestart(ctx context.Context, targetKey string, rules []*karov1alpha1.RestartRule, restartFunc func() error) {
	logger := log.FromContext(ctx).WithValues("targetKey", targetKey)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if manager is stopped
	if m.stopped {
		logger.Info("DelayedRestartManager is stopped, ignoring restart request")

		return
	}

	// Check if restart is already pending for this target
	if _, exists := m.pendingTasks[targetKey]; exists {
		logger.Info("Restart already scheduled for target, skipping duplicate request")

		return
	}

	// Calculate the maximum delay from all rules
	maxDelay := m.calculateMaxDelay(rules)

	logger.Info("Scheduling delayed restart",
		"delay", maxDelay,
		"rulesCount", len(rules))

	// Create a cancellable context for this task
	taskCtx, cancelFunc := context.WithCancel(ctx)

	// Create the restart task
	task := &restartTask{
		targetKey:   targetKey,
		delay:       maxDelay,
		restartFunc: restartFunc,
		cancelFunc:  cancelFunc,
		done:        make(chan struct{}),
	}

	// Store the task
	m.pendingTasks[targetKey] = task

	// Start the goroutine to handle the delayed restart
	m.wg.Add(1)
	go m.executeDelayedRestart(taskCtx, task)
}

// IsRestartPending checks if a restart is currently pending for the given target
func (m *DelayedRestartManagerImpl) IsRestartPending(targetKey string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, exists := m.pendingTasks[targetKey]

	return exists
}

// Stop gracefully shuts down all pending operations
func (m *DelayedRestartManagerImpl) Stop() {
	m.mu.Lock()
	// Remove the defer statement - manage manually

	if m.stopped {
		m.mu.Unlock()

		return
	}

	m.stopped = true
	close(m.stopCh)

	// Cancel all pending tasks
	for _, task := range m.pendingTasks {
		if task.cancelFunc != nil {
			task.cancelFunc()
		}
	}

	// Release the lock before waiting to avoid deadlock
	m.mu.Unlock()

	// Wait for all goroutines to finish
	m.wg.Wait()

	// Re-acquire lock to clean up
	m.mu.Lock()
	defer m.mu.Unlock() // Use defer only here for final cleanup

	// Clear pending tasks
	m.pendingTasks = make(map[string]*restartTask)
}

// GetPendingTargets returns a list of target keys that have pending restarts
// This is useful for testing and debugging
func (m *DelayedRestartManagerImpl) GetPendingTargets() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	targets := make([]string, 0, len(m.pendingTasks))
	for targetKey := range m.pendingTasks {
		targets = append(targets, targetKey)
	}

	return targets
}

// WaitForTask waits for a specific task to complete
// This is primarily for testing purposes
func (m *DelayedRestartManagerImpl) WaitForTask(targetKey string, timeout time.Duration) error {
	m.mu.RLock()
	task, exists := m.pendingTasks[targetKey]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("%w: %s", ErrTaskNotFound, targetKey)
	}

	select {
	case <-task.done:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("%w: %s", ErrTaskTimeout, targetKey)
	}
}

// calculateMaxDelay calculates the maximum delay from all rules
func (m *DelayedRestartManagerImpl) calculateMaxDelay(rules []*karov1alpha1.RestartRule) time.Duration {
	var maxDelay int32 = 0
	hasDelayRules := false

	for _, rule := range rules {
		if rule.Spec.DelayRestart != nil {
			hasDelayRules = true
			if *rule.Spec.DelayRestart > maxDelay {
				maxDelay = *rule.Spec.DelayRestart
			}
		}
	}

	// If no rules have delays, return 0
	if !hasDelayRules {
		return 0
	}

	return time.Duration(maxDelay) * time.Second
}

// executeDelayedRestart executes the delayed restart operation
func (m *DelayedRestartManagerImpl) executeDelayedRestart(ctx context.Context, task *restartTask) {
	defer m.wg.Done()
	defer close(task.done)

	logger := log.FromContext(ctx).WithValues("targetKey", task.targetKey, "delay", task.delay)

	// Clean up the task from pending tasks when done
	defer func() {
		m.mu.Lock()
		delete(m.pendingTasks, task.targetKey)
		m.mu.Unlock()
	}()

	// If there's no delay, execute immediately
	if task.delay == 0 {
		logger.Info("Executing immediate restart (no delay configured)")
		if err := task.restartFunc(); err != nil {
			logger.Error(err, "Failed to execute restart function")
		}

		return
	}

	logger.Info("Waiting for delay before restart")

	// Wait for the delay or context cancellation
	select {
	case <-time.After(task.delay):
		// Delay completed, execute the restart
		logger.Info("Delay completed, executing restart")
		if err := task.restartFunc(); err != nil {
			logger.Error(err, "Failed to execute restart function")
		} else {
			logger.Info("Successfully executed delayed restart")
		}

	case <-ctx.Done():
		// Context was cancelled
		logger.Info("Delayed restart cancelled due to context cancellation")

	case <-m.stopCh:
		// Manager is stopping
		logger.Info("Delayed restart cancelled due to manager shutdown")
	}
}
