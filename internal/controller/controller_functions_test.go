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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

// mockDelayedRestartManager is a mock implementation of DelayedRestartManager for testing
type mockDelayedRestartManager struct {
	mu                sync.RWMutex
	scheduledRestarts map[TargetKey]*mockScheduledRestart
	scheduleCallCount atomic.Int32
}

type mockScheduledRestart struct {
	target      TargetKey
	delay       time.Duration
	restartFunc RestartFunc
}

func newMockDelayedRestartManager() *mockDelayedRestartManager {
	return &mockDelayedRestartManager{
		scheduledRestarts: make(map[TargetKey]*mockScheduledRestart),
	}
}

func (m *mockDelayedRestartManager) ScheduleRestart(ctx context.Context, target TargetKey, delay time.Duration, restartFunc RestartFunc) (finalDelay time.Duration, isNew bool, err error) {
	m.scheduleCallCount.Add(1)

	m.mu.Lock()
	defer m.mu.Unlock()

	existing, exists := m.scheduledRestarts[target]
	if exists {
		if delay > existing.delay {
			m.scheduledRestarts[target] = &mockScheduledRestart{
				target:      target,
				delay:       delay,
				restartFunc: restartFunc,
			}

			return delay, true, nil
		}

		return existing.delay, false, nil
	}

	m.scheduledRestarts[target] = &mockScheduledRestart{
		target:      target,
		delay:       delay,
		restartFunc: restartFunc,
	}

	return delay, true, nil
}

func (m *mockDelayedRestartManager) IsScheduled(target TargetKey) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, exists := m.scheduledRestarts[target]

	return exists
}

func (m *mockDelayedRestartManager) Cancel(target TargetKey) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.scheduledRestarts, target)
}

func (m *mockDelayedRestartManager) Shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scheduledRestarts = make(map[TargetKey]*mockScheduledRestart)
}

func (m *mockDelayedRestartManager) GetScheduledCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.scheduledRestarts)
}

func (m *mockDelayedRestartManager) GetScheduledDelay(target TargetKey) (time.Duration, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	scheduled, exists := m.scheduledRestarts[target]
	if !exists {
		return 0, false
	}

	return scheduled.delay, true
}

func int32Ptr(i int32) *int32 {
	return &i
}

func TestBaseReconciler_groupRulesByTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		restartRules  []*karov1alpha1.RestartRule
		expectedCount int
		validate      func(t *testing.T, grouped map[targetKey][]*karov1alpha1.RestartRule)
	}{
		{
			name: "single rule single target",
			restartRules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rule1",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app1"},
						},
					},
				},
			},
			expectedCount: 1,
			validate: func(t *testing.T, grouped map[targetKey][]*karov1alpha1.RestartRule) {
				key := targetKey{kind: "Deployment", name: "app1", namespace: "default"}
				if len(grouped[key]) != 1 {
					t.Errorf("Expected 1 rule for target, got %d", len(grouped[key]))
				}
			},
		},
		{
			name: "single rule multiple targets",
			restartRules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rule1",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app1"},
							{Kind: "Deployment", Name: "app2"},
							{Kind: "StatefulSet", Name: "app3"},
						},
					},
				},
			},
			expectedCount: 3,
		},
		{
			name: "multiple rules same target",
			restartRules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rule1",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app1"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rule2",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app1"},
						},
					},
				},
			},
			expectedCount: 1,
			validate: func(t *testing.T, grouped map[targetKey][]*karov1alpha1.RestartRule) {
				key := targetKey{kind: "Deployment", name: "app1", namespace: "default"}
				if len(grouped[key]) != 2 {
					t.Errorf("Expected 2 rules for target, got %d", len(grouped[key]))
				}
			},
		},
		{
			name: "target with explicit namespace",
			restartRules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rule1",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app1", Namespace: "production"},
						},
					},
				},
			},
			expectedCount: 1,
			validate: func(t *testing.T, grouped map[targetKey][]*karov1alpha1.RestartRule) {
				key := targetKey{kind: "Deployment", name: "app1", namespace: "production"}
				if len(grouped[key]) != 1 {
					t.Errorf("Expected 1 rule for target in production namespace, got %d", len(grouped[key]))
				}
			},
		},
		{
			name:          "empty rules",
			restartRules:  []*karov1alpha1.RestartRule{},
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reconciler := &BaseReconciler{}
			grouped := reconciler.groupRulesByTarget(tt.restartRules)

			if len(grouped) != tt.expectedCount {
				t.Errorf("groupRulesByTarget() returned %d targets, want %d", len(grouped), tt.expectedCount)
			}

			if tt.validate != nil {
				tt.validate(t, grouped)
			}
		})
	}
}

func TestBaseReconciler_findMaxDelay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		rules            []*karov1alpha1.RestartRule
		expectedDelay    *int32
		expectedRuleName string
		expectedIsNil    bool
	}{
		{
			name: "single rule with delay",
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec:       karov1alpha1.RestartRuleSpec{DelayRestart: int32Ptr(60)},
				},
			},
			expectedDelay:    int32Ptr(60),
			expectedRuleName: "rule1",
		},
		{
			name: "multiple rules different delays",
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec:       karov1alpha1.RestartRuleSpec{DelayRestart: int32Ptr(30)},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule2"},
					Spec:       karov1alpha1.RestartRuleSpec{DelayRestart: int32Ptr(90)},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule3"},
					Spec:       karov1alpha1.RestartRuleSpec{DelayRestart: int32Ptr(45)},
				},
			},
			expectedDelay:    int32Ptr(90),
			expectedRuleName: "rule2",
		},
		{
			name: "rules with and without delay",
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec:       karov1alpha1.RestartRuleSpec{DelayRestart: nil},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule2"},
					Spec:       karov1alpha1.RestartRuleSpec{DelayRestart: int32Ptr(60)},
				},
			},
			expectedDelay:    int32Ptr(60),
			expectedRuleName: "rule2",
		},
		{
			name: "no rules with delay",
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec:       karov1alpha1.RestartRuleSpec{DelayRestart: nil},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule2"},
					Spec:       karov1alpha1.RestartRuleSpec{DelayRestart: nil},
				},
			},
			expectedIsNil: true,
		},
		{
			name:          "empty rules",
			rules:         []*karov1alpha1.RestartRule{},
			expectedIsNil: true,
		},
		{
			name: "delay of zero",
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1"},
					Spec:       karov1alpha1.RestartRuleSpec{DelayRestart: int32Ptr(0)},
				},
			},
			expectedDelay:    int32Ptr(0),
			expectedRuleName: "rule1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reconciler := &BaseReconciler{}
			maxDelay, maxDelayRule := reconciler.findMaxDelay(tt.rules)

			if tt.expectedIsNil {
				if maxDelay != nil {
					t.Errorf("findMaxDelay() returned non-nil delay %v, want nil", *maxDelay)
				}
				if maxDelayRule != nil {
					t.Errorf("findMaxDelay() returned non-nil rule %v, want nil", maxDelayRule.Name)
				}

				return
			}

			if maxDelay == nil {
				t.Error("findMaxDelay() returned nil delay, want non-nil")

				return
			}

			if *maxDelay != *tt.expectedDelay {
				t.Errorf("findMaxDelay() returned delay %v, want %v", *maxDelay, *tt.expectedDelay)
			}

			if maxDelayRule == nil {
				t.Error("findMaxDelay() returned nil rule, want non-nil")

				return
			}

			if maxDelayRule.Name != tt.expectedRuleName {
				t.Errorf("findMaxDelay() returned rule %v, want %v", maxDelayRule.Name, tt.expectedRuleName)
			}
		})
	}
}

func TestBaseReconciler_getTargetSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		targetKey    targetKey
		rules        []*karov1alpha1.RestartRule
		expectedKind string
		expectedName string
	}{
		{
			name: "find target in first rule",
			targetKey: targetKey{
				kind:      "Deployment",
				name:      "app1",
				namespace: "default",
			},
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rule1",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app1"},
						},
					},
				},
			},
			expectedKind: "Deployment",
			expectedName: "app1",
		},
		{
			name: "find target among multiple targets",
			targetKey: targetKey{
				kind:      "StatefulSet",
				name:      "app2",
				namespace: "default",
			},
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rule1",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app1"},
							{Kind: "StatefulSet", Name: "app2"},
							{Kind: "Deployment", Name: "app3"},
						},
					},
				},
			},
			expectedKind: "StatefulSet",
			expectedName: "app2",
		},
		{
			name: "target with explicit namespace",
			targetKey: targetKey{
				kind:      "Deployment",
				name:      "app1",
				namespace: "production",
			},
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "rule1",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app1", Namespace: "production"},
						},
					},
				},
			},
			expectedKind: "Deployment",
			expectedName: "app1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reconciler := &BaseReconciler{}
			targetSpec := reconciler.getTargetSpec(tt.targetKey, tt.rules)

			if targetSpec.Kind != tt.expectedKind {
				t.Errorf("getTargetSpec() returned Kind=%v, want %v", targetSpec.Kind, tt.expectedKind)
			}

			if targetSpec.Name != tt.expectedName {
				t.Errorf("getTargetSpec() returned Name=%v, want %v", targetSpec.Name, tt.expectedName)
			}
		})
	}
}

func TestBaseReconciler_extractRuleNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		rules         []*karov1alpha1.RestartRule
		expectedNames []string
	}{
		{
			name: "single rule",
			rules: []*karov1alpha1.RestartRule{
				{ObjectMeta: metav1.ObjectMeta{Name: "rule1"}},
			},
			expectedNames: []string{"rule1"},
		},
		{
			name: "multiple rules",
			rules: []*karov1alpha1.RestartRule{
				{ObjectMeta: metav1.ObjectMeta{Name: "rule1"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "rule2"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "rule3"}},
			},
			expectedNames: []string{"rule1", "rule2", "rule3"},
		},
		{
			name:          "empty rules",
			rules:         []*karov1alpha1.RestartRule{},
			expectedNames: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reconciler := &BaseReconciler{}
			names := reconciler.extractRuleNames(tt.rules)

			if len(names) != len(tt.expectedNames) {
				t.Errorf("extractRuleNames() returned %d names, want %d", len(names), len(tt.expectedNames))

				return
			}

			for i, name := range names {
				if name != tt.expectedNames[i] {
					t.Errorf("extractRuleNames()[%d] = %v, want %v", i, name, tt.expectedNames[i])
				}
			}
		})
	}
}

// TestBaseReconciler_ProcessRestartRules_NoDelay is skipped because it requires a real Kubernetes client
// to test immediate restart behavior. The delay scheduling logic is thoroughly tested in other tests.
// This test would verify that rules without delay do NOT schedule delayed restarts, but execute immediately.
func TestBaseReconciler_ProcessRestartRules_NoDelay(t *testing.T) {
	t.Skip("Skipping test that requires Kubernetes client for immediate restart execution")
}

func TestBaseReconciler_ProcessRestartRules_WithDelay(t *testing.T) {
	t.Parallel()

	mockManager := newMockDelayedRestartManager()
	reconciler := &BaseReconciler{
		DelayedRestartManager: mockManager,
	}

	ctx := context.Background()
	restartRules := []*karov1alpha1.RestartRule{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rule1",
				Namespace: "default",
			},
			Spec: karov1alpha1.RestartRuleSpec{
				DelayRestart: int32Ptr(60),
				Targets: []karov1alpha1.TargetSpec{
					{Kind: "Deployment", Name: "app1"},
				},
			},
		},
	}

	err := reconciler.ProcessRestartRules(ctx, restartRules, "test-configmap", "ConfigMap")
	if err != nil {
		t.Errorf("ProcessRestartRules() with delay returned unexpected error: %v", err)
	}

	// Verify delayed restart was scheduled
	if mockManager.GetScheduledCount() != 1 {
		t.Errorf("ProcessRestartRules() scheduled %d restarts, want 1", mockManager.GetScheduledCount())
	}

	// Verify correct target and delay
	target := TargetKey{Kind: "Deployment", Name: "app1", Namespace: "default"}
	delay, exists := mockManager.GetScheduledDelay(target)
	if !exists {
		t.Error("ProcessRestartRules() did not schedule restart for expected target")
	}
	expectedDelay := 60 * time.Second
	if delay != expectedDelay {
		t.Errorf("ProcessRestartRules() scheduled delay %v, want %v", delay, expectedDelay)
	}
}

func TestBaseReconciler_ProcessRestartRules_MultipleRulesSameTarget(t *testing.T) {
	t.Parallel()

	mockManager := newMockDelayedRestartManager()
	reconciler := &BaseReconciler{
		DelayedRestartManager: mockManager,
	}

	ctx := context.Background()
	restartRules := []*karov1alpha1.RestartRule{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rule1",
				Namespace: "default",
			},
			Spec: karov1alpha1.RestartRuleSpec{
				DelayRestart: int32Ptr(30),
				Targets: []karov1alpha1.TargetSpec{
					{Kind: "Deployment", Name: "app1"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rule2",
				Namespace: "default",
			},
			Spec: karov1alpha1.RestartRuleSpec{
				DelayRestart: int32Ptr(90),
				Targets: []karov1alpha1.TargetSpec{
					{Kind: "Deployment", Name: "app1"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rule3",
				Namespace: "default",
			},
			Spec: karov1alpha1.RestartRuleSpec{
				DelayRestart: int32Ptr(45),
				Targets: []karov1alpha1.TargetSpec{
					{Kind: "Deployment", Name: "app1"},
				},
			},
		},
	}

	err := reconciler.ProcessRestartRules(ctx, restartRules, "test-configmap", "ConfigMap")
	if err != nil {
		t.Errorf("ProcessRestartRules() returned unexpected error: %v", err)
	}

	// Should have scheduled only one restart (not 3)
	if mockManager.GetScheduledCount() != 1 {
		t.Errorf("ProcessRestartRules() scheduled %d restarts, want 1 (grouped by target)", mockManager.GetScheduledCount())
	}

	// Verify the highest delay was used (90 seconds)
	target := TargetKey{Kind: "Deployment", Name: "app1", Namespace: "default"}
	delay, exists := mockManager.GetScheduledDelay(target)
	if !exists {
		t.Error("ProcessRestartRules() did not schedule restart for expected target")
	}
	expectedDelay := 90 * time.Second
	if delay != expectedDelay {
		t.Errorf("ProcessRestartRules() scheduled delay %v, want %v (highest delay)", delay, expectedDelay)
	}
}

func TestBaseReconciler_ProcessRestartRules_MixedDelayAndNoDelay(t *testing.T) {
	t.Parallel()

	mockManager := newMockDelayedRestartManager()
	reconciler := &BaseReconciler{
		DelayedRestartManager: mockManager,
	}

	ctx := context.Background()
	restartRules := []*karov1alpha1.RestartRule{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rule1",
				Namespace: "default",
			},
			Spec: karov1alpha1.RestartRuleSpec{
				DelayRestart: nil, // No delay
				Targets: []karov1alpha1.TargetSpec{
					{Kind: "Deployment", Name: "app1"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rule2",
				Namespace: "default",
			},
			Spec: karov1alpha1.RestartRuleSpec{
				DelayRestart: int32Ptr(60),
				Targets: []karov1alpha1.TargetSpec{
					{Kind: "Deployment", Name: "app1"},
				},
			},
		},
	}

	err := reconciler.ProcessRestartRules(ctx, restartRules, "test-configmap", "ConfigMap")
	if err != nil {
		t.Errorf("ProcessRestartRules() returned unexpected error: %v", err)
	}

	// When mixed, delay should win
	if mockManager.GetScheduledCount() != 1 {
		t.Errorf("ProcessRestartRules() scheduled %d restarts, want 1", mockManager.GetScheduledCount())
	}

	target := TargetKey{Kind: "Deployment", Name: "app1", Namespace: "default"}
	delay, exists := mockManager.GetScheduledDelay(target)
	if !exists {
		t.Error("ProcessRestartRules() did not schedule restart for expected target")
	}
	expectedDelay := 60 * time.Second
	if delay != expectedDelay {
		t.Errorf("ProcessRestartRules() scheduled delay %v, want %v (delay should win)", delay, expectedDelay)
	}
}

func TestBaseReconciler_ProcessRestartRules_MultipleTargets(t *testing.T) {
	t.Parallel()

	mockManager := newMockDelayedRestartManager()
	reconciler := &BaseReconciler{
		DelayedRestartManager: mockManager,
	}

	ctx := context.Background()
	restartRules := []*karov1alpha1.RestartRule{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rule1",
				Namespace: "default",
			},
			Spec: karov1alpha1.RestartRuleSpec{
				DelayRestart: int32Ptr(60),
				Targets: []karov1alpha1.TargetSpec{
					{Kind: "Deployment", Name: "app1"},
					{Kind: "Deployment", Name: "app2"},
					{Kind: "StatefulSet", Name: "app3"},
				},
			},
		},
	}

	err := reconciler.ProcessRestartRules(ctx, restartRules, "test-configmap", "ConfigMap")
	if err != nil {
		t.Errorf("ProcessRestartRules() returned unexpected error: %v", err)
	}

	// Should schedule restarts for all 3 targets
	if mockManager.GetScheduledCount() != 3 {
		t.Errorf("ProcessRestartRules() scheduled %d restarts, want 3", mockManager.GetScheduledCount())
	}

	// Verify each target
	targets := []TargetKey{
		{Kind: "Deployment", Name: "app1", Namespace: "default"},
		{Kind: "Deployment", Name: "app2", Namespace: "default"},
		{Kind: "StatefulSet", Name: "app3", Namespace: "default"},
	}
	expectedDelay := 60 * time.Second

	for _, target := range targets {
		delay, exists := mockManager.GetScheduledDelay(target)
		if !exists {
			t.Errorf("ProcessRestartRules() did not schedule restart for target %v", target)
		}
		if delay != expectedDelay {
			t.Errorf("ProcessRestartRules() scheduled delay %v for target %v, want %v", delay, target, expectedDelay)
		}
	}
}

// TestBaseReconciler_ProcessRestartRules_ZeroDelay is skipped because it requires a real Kubernetes client
// to test immediate restart behavior (delay=0 triggers immediate restart).
func TestBaseReconciler_ProcessRestartRules_ZeroDelay(t *testing.T) {
	t.Skip("Skipping test that requires Kubernetes client for immediate restart execution (delay=0)")
}

func TestBaseReconciler_ProcessRestartRules_EmptyRules(t *testing.T) {
	t.Parallel()

	mockManager := newMockDelayedRestartManager()
	reconciler := &BaseReconciler{
		DelayedRestartManager: mockManager,
	}

	ctx := context.Background()
	restartRules := []*karov1alpha1.RestartRule{}

	err := reconciler.ProcessRestartRules(ctx, restartRules, "test-configmap", "ConfigMap")
	if err != nil {
		t.Errorf("ProcessRestartRules() with empty rules returned error: %v", err)
	}

	if mockManager.GetScheduledCount() != 0 {
		t.Errorf("ProcessRestartRules() with empty rules scheduled %d restarts, want 0", mockManager.GetScheduledCount())
	}
}

func TestBaseReconciler_ProcessRestartRules_DeploymentAndStatefulSet(t *testing.T) {
	t.Parallel()

	mockManager := newMockDelayedRestartManager()
	reconciler := &BaseReconciler{
		DelayedRestartManager: mockManager,
	}

	ctx := context.Background()
	restartRules := []*karov1alpha1.RestartRule{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rule1",
				Namespace: "default",
			},
			Spec: karov1alpha1.RestartRuleSpec{
				DelayRestart: int32Ptr(60),
				Targets: []karov1alpha1.TargetSpec{
					{Kind: "Deployment", Name: "app1"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rule2",
				Namespace: "default",
			},
			Spec: karov1alpha1.RestartRuleSpec{
				DelayRestart: int32Ptr(90),
				Targets: []karov1alpha1.TargetSpec{
					{Kind: "StatefulSet", Name: "app2"},
				},
			},
		},
	}

	err := reconciler.ProcessRestartRules(ctx, restartRules, "test-secret", "Secret")
	if err != nil {
		t.Errorf("ProcessRestartRules() returned unexpected error: %v", err)
	}

	// Should schedule restarts for both targets with their respective delays
	if mockManager.GetScheduledCount() != 2 {
		t.Errorf("ProcessRestartRules() scheduled %d restarts, want 2", mockManager.GetScheduledCount())
	}

	// Verify Deployment
	deploymentTarget := TargetKey{Kind: "Deployment", Name: "app1", Namespace: "default"}
	deploymentDelay, exists := mockManager.GetScheduledDelay(deploymentTarget)
	if !exists {
		t.Error("ProcessRestartRules() did not schedule restart for Deployment")
	}
	if deploymentDelay != 60*time.Second {
		t.Errorf("ProcessRestartRules() scheduled delay %v for Deployment, want %v", deploymentDelay, 60*time.Second)
	}

	// Verify StatefulSet
	statefulSetTarget := TargetKey{Kind: "StatefulSet", Name: "app2", Namespace: "default"}
	statefulSetDelay, exists := mockManager.GetScheduledDelay(statefulSetTarget)
	if !exists {
		t.Error("ProcessRestartRules() did not schedule restart for StatefulSet")
	}
	if statefulSetDelay != 90*time.Second {
		t.Errorf("ProcessRestartRules() scheduled delay %v for StatefulSet, want %v", statefulSetDelay, 90*time.Second)
	}
}

func TestGetConfigMapInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		configMapName     string
		configMapNS       string
		expectedName      string
		expectedNamespace string
		expectedType      string
	}{
		{
			name:              "standard configmap",
			configMapName:     "my-config",
			configMapNS:       "default",
			expectedName:      "my-config",
			expectedNamespace: "default",
			expectedType:      "ConfigMap",
		},
		{
			name:              "configmap in different namespace",
			configMapName:     "app-config",
			configMapNS:       "production",
			expectedName:      "app-config",
			expectedNamespace: "production",
			expectedType:      "ConfigMap",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			configMap := corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.configMapName,
					Namespace: tt.configMapNS,
				},
			}

			info := GetConfigMapInfo(configMap)

			if info.Name != tt.expectedName {
				t.Errorf("GetConfigMapInfo().Name = %v, want %v", info.Name, tt.expectedName)
			}
			if info.Namespace != tt.expectedNamespace {
				t.Errorf("GetConfigMapInfo().Namespace = %v, want %v", info.Namespace, tt.expectedNamespace)
			}
			if info.Type != tt.expectedType {
				t.Errorf("GetConfigMapInfo().Type = %v, want %v", info.Type, tt.expectedType)
			}
		})
	}
}

func TestGetSecretInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		secretName        string
		secretNS          string
		expectedName      string
		expectedNamespace string
		expectedType      string
	}{
		{
			name:              "standard secret",
			secretName:        "my-secret",
			secretNS:          "default",
			expectedName:      "my-secret",
			expectedNamespace: "default",
			expectedType:      "Secret",
		},
		{
			name:              "secret in different namespace",
			secretName:        "app-secret",
			secretNS:          "production",
			expectedName:      "app-secret",
			expectedNamespace: "production",
			expectedType:      "Secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			secret := corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.secretName,
					Namespace: tt.secretNS,
				},
			}

			info := GetSecretInfo(secret)

			if info.Name != tt.expectedName {
				t.Errorf("GetSecretInfo().Name = %v, want %v", info.Name, tt.expectedName)
			}
			if info.Namespace != tt.expectedNamespace {
				t.Errorf("GetSecretInfo().Namespace = %v, want %v", info.Namespace, tt.expectedNamespace)
			}
			if info.Type != tt.expectedType {
				t.Errorf("GetSecretInfo().Type = %v, want %v", info.Type, tt.expectedType)
			}
		})
	}
}
