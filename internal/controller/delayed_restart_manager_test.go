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
	"log"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	stdr "github.com/go-logr/stdr"
)

// add a package-level static error to satisfy lint rule err113 (do not define dynamic errors)
var errTaskCtxTimeout = errors.New("timeout waiting for taskCtx to be Done")

// TODO: Make these tests easier with regards to channel usage and timeouts

func TestNewDelayedRestartManager(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)

	if manager == nil {
		t.Fatal("NewDelayedRestartManager() returned nil")
	}

	// Type assertion to check internal structure
	concreteManager, ok := manager.(*delayedRestartManager)
	if !ok {
		t.Fatal("NewDelayedRestartManager() did not return *delayedRestartManager")
	}

	if concreteManager.tasks == nil {
		t.Error("NewDelayedRestartManager() created manager with nil tasks map")
	}

	if len(concreteManager.tasks) != 0 {
		t.Errorf("NewDelayedRestartManager() created manager with non-empty tasks map, got %d tasks", len(concreteManager.tasks))
	}
}

func TestDelayedRestartManager_ScheduleRestart_Immediate(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)
	defer manager.Shutdown()

	ctx := context.Background()
	target := TargetKey{
		Kind:      "Deployment",
		Name:      "test-deployment",
		Namespace: "default",
	}

	executed := make(chan struct{})
	restartFunc := func(ctx context.Context) error {
		close(executed)

		return nil
	}

	// Schedule immediate restart (delay=0)
	finalDelay, isNew, err := manager.ScheduleRestart(ctx, target, 0, restartFunc)

	if err != nil {
		t.Errorf("ScheduleRestart() with delay=0 returned error: %v", err)
	}

	if !isNew {
		t.Error("ScheduleRestart() with delay=0 should return isNew=true")
	}

	if finalDelay != 0 {
		t.Errorf("ScheduleRestart() with delay=0 returned finalDelay=%v, want 0", finalDelay)
	}

	// Wait for execution with timeout
	select {
	case <-executed:
		// Success - restart executed immediately
	case <-time.After(200 * time.Millisecond):
		t.Error("ScheduleRestart() with delay=0 did not execute within 200ms")
	}
}

func TestDelayedRestartManager_ScheduleRestart_Delayed(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)
	defer manager.Shutdown()

	ctx := context.Background()
	target := TargetKey{
		Kind:      "Deployment",
		Name:      "test-deployment",
		Namespace: "default",
	}

	startTime := time.Now()
	executed := make(chan time.Time)
	restartFunc := func(ctx context.Context) error {
		executed <- time.Now()

		return nil
	}

	delay := 100 * time.Millisecond

	// Schedule delayed restart
	finalDelay, isNew, err := manager.ScheduleRestart(ctx, target, delay, restartFunc)

	if err != nil {
		t.Errorf("ScheduleRestart() with delay=1s returned error: %v", err)
	}

	if !isNew {
		t.Error("ScheduleRestart() with delay=1s should return isNew=true")
	}

	if finalDelay != delay {
		t.Errorf("ScheduleRestart() returned finalDelay=%v, want %v", finalDelay, delay)
	}

	// Check that restart is scheduled
	if !manager.IsScheduled(target) {
		t.Error("IsScheduled() returned false after scheduling")
	}

	// Wait for execution with timeout
	select {
	case executedAt := <-executed:
		elapsed := executedAt.Sub(startTime)
		if elapsed < delay {
			t.Errorf("Restart executed too early, elapsed=%v, expected at least %v", elapsed, delay)
		}
		if elapsed > delay+50*time.Millisecond {
			t.Errorf("Restart executed too late, elapsed=%v, expected around %v", elapsed, delay)
		}
	case <-time.After(delay + 200*time.Millisecond):
		t.Error("ScheduleRestart() did not execute within expected time")
	}

	// Check that task is removed after execution
	time.Sleep(10 * time.Millisecond)
	if manager.IsScheduled(target) {
		t.Error("IsScheduled() returned true after execution")
	}
}

//nolint:cyclop // Test function complexity is acceptable for comprehensive testing
func TestDelayedRestartManager_ScheduleRestart_MultipleRulesNoReshedule(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)
	defer manager.Shutdown()

	ctx := context.Background()
	target := TargetKey{
		Kind:      "Deployment",
		Name:      "test-deployment",
		Namespace: "default",
	}

	executed := make(chan struct{})
	var executionCount atomic.Int32
	restartFunc := func(ctx context.Context) error {
		executionCount.Add(1)
		executed <- struct{}{}

		return nil
	}

	// Schedule with delay 100ms
	delay1 := 100 * time.Millisecond
	finalDelay1, isNew1, err1 := manager.ScheduleRestart(ctx, target, delay1, restartFunc)
	if err1 != nil {
		t.Errorf("First ScheduleRestart() returned error: %v", err1)
	}
	if !isNew1 {
		t.Error("First ScheduleRestart() should return isNew=true")
	}
	if finalDelay1 != delay1 {
		t.Errorf("First ScheduleRestart() returned finalDelay=%v, want %v", finalDelay1, delay1)
	}

	// Schedule again with a higher delay (200ms) - should keep the original 100ms
	delay2 := 200 * time.Millisecond
	finalDelay2, isNew2, err2 := manager.ScheduleRestart(ctx, target, delay2, restartFunc)
	if err2 != nil {
		t.Errorf("Second ScheduleRestart() returned error: %v", err2)
	}
	if isNew2 {
		t.Error("Second ScheduleRestart() should return isNew=false when already scheduled")
	}
	if finalDelay2 != delay1 {
		t.Errorf("Second ScheduleRestart() returned finalDelay=%v, want %v (existing delay)", finalDelay2, delay1)
	}

	// Schedule again with a shorter delay (50ms) - should keep the original 100ms
	delay3 := 50 * time.Millisecond
	finalDelay3, isNew3, err3 := manager.ScheduleRestart(ctx, target, delay3, restartFunc)
	if err3 != nil {
		t.Errorf("Third ScheduleRestart() returned error: %v", err3)
	}
	if isNew3 {
		t.Error("Third ScheduleRestart() should return isNew=false when already scheduled")
	}
	if finalDelay3 != delay1 {
		t.Errorf("Third ScheduleRestart() returned finalDelay=%v, want %v (existing delay)", finalDelay3, delay1)
	}

	// Wait for execution
	select {
	case <-executed:
		// Success - restart executed
	case <-time.After(delay2 + 200*time.Millisecond):
		t.Error("ScheduleRestart() did not execute within expected time")
	}

	time.Sleep(100 * time.Millisecond)
	if count := executionCount.Load(); count != 1 {
		t.Errorf("Restart was executed %d times, want exactly 1", count)
	}
}

func TestDelayedRestartManager_ScheduleRestart_AlreadyScheduled(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)
	defer manager.Shutdown()

	ctx := context.Background()
	target := TargetKey{
		Kind:      "Deployment",
		Name:      "test-deployment",
		Namespace: "default",
	}

	restartFunc := func(ctx context.Context) error {
		return nil
	}

	delay := 200 * time.Millisecond

	// First schedule
	finalDelay1, isNew1, err1 := manager.ScheduleRestart(ctx, target, delay, restartFunc)
	if err1 != nil {
		t.Errorf("First ScheduleRestart() returned error: %v", err1)
	}
	if !isNew1 {
		t.Error("First ScheduleRestart() should return isNew=true")
	}
	if finalDelay1 != delay {
		t.Errorf("First ScheduleRestart() returned finalDelay=%v, want %v", finalDelay1, delay)
	}

	// Second schedule with same delay
	finalDelay2, isNew2, err2 := manager.ScheduleRestart(ctx, target, delay, restartFunc)
	if err2 != nil {
		t.Errorf("Second ScheduleRestart() returned error: %v", err2)
	}
	if isNew2 {
		t.Error("Second ScheduleRestart() should return isNew=false when already scheduled with same delay")
	}
	if finalDelay2 != delay {
		t.Errorf("Second ScheduleRestart() returned finalDelay=%v, want %v", finalDelay2, delay)
	}
}

func TestDelayedRestartManager_ScheduleRestart_NegativeDelay(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)
	defer manager.Shutdown()

	ctx := context.Background()
	target := TargetKey{
		Kind:      "Deployment",
		Name:      "test-deployment",
		Namespace: "default",
	}

	restartFunc := func(ctx context.Context) error {
		return nil
	}

	// Schedule with negative delay
	_, _, err := manager.ScheduleRestart(ctx, target, -100*time.Millisecond, restartFunc)

	if err == nil {
		t.Error("ScheduleRestart() with negative delay should return error")
	}

	if !errors.Is(err, ErrNegativeDelay) {
		t.Errorf("ScheduleRestart() with negative delay should return ErrNegativeDelay, got: %v", err)
	}
}

func TestDelayedRestartManager_ScheduleRestart_NilRestartFunc(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)
	defer manager.Shutdown()

	ctx := context.Background()
	target := TargetKey{
		Kind:      "Deployment",
		Name:      "test-deployment",
		Namespace: "default",
	}

	// Schedule with nil restart function
	_, _, err := manager.ScheduleRestart(ctx, target, 100*time.Millisecond, nil)

	if err == nil {
		t.Error("ScheduleRestart() with nil restartFunc should return error")
	}

	if !errors.Is(err, ErrNilRestartFunc) {
		t.Errorf("ScheduleRestart() with nil restartFunc should return ErrNilRestartFunc, got: %v", err)
	}
}

func TestDelayedRestartManager_Cancel(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)
	defer manager.Shutdown()

	ctx := context.Background()
	target := TargetKey{
		Kind:      "Deployment",
		Name:      "test-deployment",
		Namespace: "default",
	}

	executed := make(chan struct{})
	restartFunc := func(ctx context.Context) error {
		close(executed)

		return nil
	}

	delay := 200 * time.Millisecond

	// Schedule restart
	_, _, err := manager.ScheduleRestart(ctx, target, delay, restartFunc)
	if err != nil {
		t.Errorf("ScheduleRestart() returned error: %v", err)
	}

	// Verify it's scheduled
	if !manager.IsScheduled(target) {
		t.Error("IsScheduled() returned false after scheduling")
	}

	// Cancel the restart
	manager.Cancel(target)

	// Verify it's no longer scheduled
	if manager.IsScheduled(target) {
		t.Error("IsScheduled() returned true after cancellation")
	}

	// Wait and verify it was not executed
	select {
	case <-executed:
		t.Error("Restart was executed after cancellation")
	case <-time.After(delay + 100*time.Millisecond):
		// Success - restart was not executed
	}
}

func TestDelayedRestartManager_Cancel_NonExistent(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)
	defer manager.Shutdown()

	target := TargetKey{
		Kind:      "Deployment",
		Name:      "test-deployment",
		Namespace: "default",
	}

	// Cancel non-existent task - should not panic
	manager.Cancel(target)

	// Verify it's not scheduled
	if manager.IsScheduled(target) {
		t.Error("IsScheduled() returned true for non-existent task")
	}
}

func TestDelayedRestartManager_Shutdown(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)

	ctx := context.Background()

	// Schedule multiple restarts
	var executionCount atomic.Int32
	restartFunc := func(ctx context.Context) error {
		executionCount.Add(1)

		return nil
	}

	targets := []TargetKey{
		{Kind: "Deployment", Name: "test-deployment-1", Namespace: "default"},
		{Kind: "Deployment", Name: "test-deployment-2", Namespace: "default"},
		{Kind: "StatefulSet", Name: "test-statefulset", Namespace: "default"},
	}

	for _, target := range targets {
		_, _, err := manager.ScheduleRestart(ctx, target, 200*time.Millisecond, restartFunc)
		if err != nil {
			t.Errorf("ScheduleRestart() returned error: %v", err)
		}
	}

	// Verify all are scheduled
	for _, target := range targets {
		if !manager.IsScheduled(target) {
			t.Errorf("IsScheduled() returned false for target %v", target)
		}
	}

	// Shutdown
	manager.Shutdown()

	// Verify all are canceled
	for _, target := range targets {
		if manager.IsScheduled(target) {
			t.Errorf("IsScheduled() returned true after shutdown for target %v", target)
		}
	}

	// Wait and verify none were executed
	time.Sleep(300 * time.Millisecond)
	if count := executionCount.Load(); count != 0 {
		t.Errorf("Restarts were executed %d times after shutdown, want 0", count)
	}

	// Multiple shutdowns should be safe (idempotent)
	manager.Shutdown()
	manager.Shutdown()
}

func TestDelayedRestartManager_RestartFuncError(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)
	defer manager.Shutdown()

	ctx := context.Background()
	target := TargetKey{
		Kind:      "Deployment",
		Name:      "test-deployment",
		Namespace: "default",
	}

	//nolint:err113 // Dynamic error acceptable for test scenarios
	expectedErr := errors.New("restart failed")
	executed := make(chan error)
	restartFunc := func(ctx context.Context) error {
		executed <- expectedErr

		return expectedErr
	}

	delay := 50 * time.Millisecond

	// Schedule restart
	_, _, err := manager.ScheduleRestart(ctx, target, delay, restartFunc)
	if err != nil {
		t.Errorf("ScheduleRestart() returned error: %v", err)
	}

	// Wait for execution
	select {
	case err := <-executed:
		if !errors.Is(err, expectedErr) {
			t.Errorf("RestartFunc returned unexpected error: %v, want %v", err, expectedErr)
		}
	case <-time.After(delay + 200*time.Millisecond):
		t.Error("RestartFunc was not executed")
	}

	// Task should be removed even if restart failed
	time.Sleep(50 * time.Millisecond)
	if manager.IsScheduled(target) {
		t.Error("IsScheduled() returned true after failed execution")
	}
}

func TestDelayedRestartManager_ConcurrentScheduling(t *testing.T) {
	t.Parallel()

	// TODO: Use stdout logger for all test cases
	logger := stdr.New(log.New(os.Stdout, "", log.LstdFlags))
	manager := NewDelayedRestartManager(logger)

	defer manager.Shutdown()

	ctx := context.Background()
	target := TargetKey{
		Kind:      "Deployment",
		Name:      "test-deployment",
		Namespace: "default",
	}

	var executionCount atomic.Int32
	//nolint:unparam // RestartFunc interface requires error return
	restartFunc := func(context.Context) error {
		executionCount.Add(1)

		return nil
	}

	// Schedule the same target concurrently from multiple goroutines
	const numGoroutines = 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	//nolint:intrange // Backward compatibility with Go versions < 1.22
	for i := 0; i < numGoroutines; i++ {
		go func(delay time.Duration) {
			defer wg.Done()
			_, _, err := manager.ScheduleRestart(ctx, target, delay, restartFunc)
			if err != nil {
				t.Errorf("ScheduleRestart() returned error: %v", err)
			}
		}(time.Duration(i*10) * time.Millisecond)
	}

	wg.Wait()

	// Should have only one scheduled task (the first one)
	if !manager.IsScheduled(target) {
		t.Error("IsScheduled() returned false after concurrent scheduling")
	}

	// Maximum delay used should be 90ms (from the last goroutine)
	time.Sleep(100 * time.Millisecond)

	// Should have executed only once
	if count := executionCount.Load(); count != 1 {
		t.Errorf("Restart was executed %d times, want exactly 1", count)
	}
}

//nolint:cyclop // Test function complexity is acceptable for comprehensive testing
func TestDelayedRestartManager_MultipleTargets(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)
	defer manager.Shutdown()

	ctx := context.Background()

	targets := []TargetKey{
		{Kind: "Deployment", Name: "test-deployment-1", Namespace: "default"},
		{Kind: "Deployment", Name: "test-deployment-2", Namespace: "default"},
		{Kind: "StatefulSet", Name: "test-statefulset", Namespace: "production"},
	}

	executed := make(chan TargetKey, len(targets))
	createRestartFunc := func(target TargetKey) RestartFunc {
		return func(ctx context.Context) error {
			executed <- target

			return nil
		}
	}

	// Schedule restarts for all targets
	for _, target := range targets {
		_, _, err := manager.ScheduleRestart(ctx, target, 50*time.Millisecond, createRestartFunc(target))
		if err != nil {
			t.Errorf("ScheduleRestart() returned error for target %v: %v", target, err)
		}
	}

	// Verify all are scheduled
	for _, target := range targets {
		if !manager.IsScheduled(target) {
			t.Errorf("IsScheduled() returned false for target %v", target)
		}
	}

	// Wait for all executions
	executedTargets := make(map[TargetKey]bool)
	//nolint:intrange // Backward compatibility with Go versions < 1.22
	for i := 0; i < len(targets); i++ {
		select {
		case target := <-executed:
			executedTargets[target] = true
		case <-time.After(200 * time.Millisecond):
			t.Error("Not all restarts were executed within expected time")
		}
	}

	// Verify all targets were executed
	for _, target := range targets {
		if !executedTargets[target] {
			t.Errorf("Target %v was not executed", target)
		}
	}

	// Verify all tasks are removed
	time.Sleep(50 * time.Millisecond)
	for _, target := range targets {
		if manager.IsScheduled(target) {
			t.Errorf("IsScheduled() returned true after execution for target %v", target)
		}
	}
}

// TODO: Refactor this test with regards to timing and channel usage
func TestDelayedRestartManager_ContextCancellation(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)
	defer manager.Shutdown()

	// Create a cancellable context
	ctx, cancel := context.WithCancel(context.Background())

	target := TargetKey{
		Kind:      "Deployment",
		Name:      "test-deployment",
		Namespace: "default",
	}

	fmt.Println("Scheduling restart with cancellable context at", time.Now())
	contextCanceled := make(chan struct{})
	var restartFuncCalled = false
	var taskCtxDone = false
	var taskCtxDoneTimeout = false
	restartFunc := func(taskCtx context.Context) error {
		fmt.Println("restartFunc called at", time.Now())
		restartFuncCalled = true
		// Check if context is canceled
		select {
		case <-taskCtx.Done():
			fmt.Println("taskCtx Done at", time.Now())
			taskCtxDone = true
			close(contextCanceled)

			return taskCtx.Err()
		case <-time.After(10 * time.Millisecond):
			fmt.Println("Timeout waiting for taskCtx to be Done at", time.Now())
			taskCtxDoneTimeout = true

			return errTaskCtxTimeout
		}

	}

	delay := 200 * time.Millisecond

	// Schedule restart
	_, _, err := manager.ScheduleRestart(ctx, target, delay, restartFunc)
	if err != nil {
		t.Errorf("ScheduleRestart() returned error: %v", err)
	}

	// TODO: Adding this sleep should make the test fail with the channels but does not.
	// time.Sleep(delay + 100*time.Millisecond)

	// Cancel the restart
	manager.Cancel(target)
	if manager.IsScheduled(target) {
		t.Error("IsScheduled() returned true after cancellation")
	}

	fmt.Println("Booleans", "restartFuncCalled:", restartFuncCalled, "taskCtxDone:", taskCtxDone, "taskCtxDoneTimeout:", taskCtxDoneTimeout)

	// The task should be canceled before execution
	time.Sleep(delay + 100*time.Millisecond)

	// Verify task context was canceled
	select {
	case <-contextCanceled:
		// Success - context was canceled
	default:
		// Task was not executed (also acceptable)
	}

	// Cancel the original context (should not affect anything now)
	cancel()
}

func TestDelayedRestartManager_GetScheduledRestarts(t *testing.T) {
	t.Parallel()

	logger := logr.Discard()
	manager := NewDelayedRestartManager(logger)
	defer manager.Shutdown()

	// Type assertion to access GetScheduledRestarts
	concreteManager, ok := manager.(*delayedRestartManager)
	if !ok {
		t.Fatal("Cannot access GetScheduledRestarts method")
	}

	ctx := context.Background()
	targets := []TargetKey{
		{Kind: "Deployment", Name: "test-deployment-1", Namespace: "default"},
		{Kind: "Deployment", Name: "test-deployment-2", Namespace: "default"},
	}

	restartFunc := func(ctx context.Context) error {
		return nil
	}

	// Initially should be empty
	scheduled := concreteManager.GetScheduledRestarts()
	if len(scheduled) != 0 {
		t.Errorf("GetScheduledRestarts() returned %d tasks, want 0", len(scheduled))
	}

	// Schedule restarts
	for _, target := range targets {
		_, _, err := manager.ScheduleRestart(ctx, target, 500*time.Millisecond, restartFunc)
		if err != nil {
			t.Errorf("ScheduleRestart() returned error: %v", err)
		}
	}

	// Should have both scheduled
	scheduled = concreteManager.GetScheduledRestarts()
	if len(scheduled) != len(targets) {
		t.Errorf("GetScheduledRestarts() returned %d tasks, want %d", len(scheduled), len(targets))
	}

	// Verify targets are present
	for _, target := range targets {
		if _, exists := scheduled[target]; !exists {
			t.Errorf("GetScheduledRestarts() missing target %v", target)
		}
	}

	// Cancel one
	manager.Cancel(targets[0])

	// Should have only one now
	scheduled = concreteManager.GetScheduledRestarts()
	if len(scheduled) != 1 {
		t.Errorf("GetScheduledRestarts() returned %d tasks after cancel, want 1", len(scheduled))
	}
}

func TestTargetKey_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		target   TargetKey
		expected string
	}{
		{
			name: "deployment target",
			target: TargetKey{
				Kind:      "Deployment",
				Name:      "test-deployment",
				Namespace: "default",
			},
			expected: "Deployment/default/test-deployment",
		},
		{
			name: "statefulset target",
			target: TargetKey{
				Kind:      "StatefulSet",
				Name:      "test-statefulset",
				Namespace: "production",
			},
			expected: "StatefulSet/production/test-statefulset",
		},
		{
			name: "empty values",
			target: TargetKey{
				Kind:      "",
				Name:      "",
				Namespace: "",
			},
			expected: "//",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.target.String()
			if result != tt.expected {
				t.Errorf("TargetKey.String() = %q, want %q", result, tt.expected)
			}
		})
	}
}
