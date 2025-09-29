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
	"fmt"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"karo.jeeatwork.com/internal/store"
)

// MockDelayedRestartManager implements DelayedRestartManager for testing
type MockDelayedRestartManager struct {
	mu               sync.RWMutex
	scheduleCallArgs []ScheduleCallArgs
	pendingTargets   map[string]bool
	shouldFail       bool
	stopped          bool
}

type ScheduleCallArgs struct {
	TargetKey    string
	Rules        []*karov1alpha1.RestartRule
	RestartFunc  func() error
	MaxDelay     time.Duration // calculated from rules
	RulesCount   int
	HasDelayRule bool
}

func NewMockDelayedRestartManager() *MockDelayedRestartManager {
	return &MockDelayedRestartManager{
		scheduleCallArgs: make([]ScheduleCallArgs, 0),
		pendingTargets:   make(map[string]bool),
	}
}

func (m *MockDelayedRestartManager) ScheduleRestart(ctx context.Context, targetKey string, rules []*karov1alpha1.RestartRule, restartFunc func() error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopped {
		return
	}

	// Calculate max delay from rules for verification
	var maxDelay int32 = 0
	hasDelayRule := false
	for _, rule := range rules {
		if rule.Spec.DelayRestart != nil {
			hasDelayRule = true
			if *rule.Spec.DelayRestart > maxDelay {
				maxDelay = *rule.Spec.DelayRestart
			}
		}
	}

	args := ScheduleCallArgs{
		TargetKey:    targetKey,
		Rules:        rules,
		RestartFunc:  restartFunc,
		MaxDelay:     time.Duration(maxDelay) * time.Second,
		RulesCount:   len(rules),
		HasDelayRule: hasDelayRule,
	}

	m.scheduleCallArgs = append(m.scheduleCallArgs, args)
	m.pendingTargets[targetKey] = true

	// Don't execute immediately in the mock - let the test control execution
	// This prevents double execution in tests
}

func (m *MockDelayedRestartManager) IsRestartPending(targetKey string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.pendingTargets[targetKey]
}

func (m *MockDelayedRestartManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
}

func (m *MockDelayedRestartManager) GetScheduleCallArgs() []ScheduleCallArgs {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return append([]ScheduleCallArgs(nil), m.scheduleCallArgs...)
}

func (m *MockDelayedRestartManager) SetShouldFail(shouldFail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shouldFail = shouldFail
}

func (m *MockDelayedRestartManager) ClearCallArgs() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.scheduleCallArgs = make([]ScheduleCallArgs, 0)
	m.pendingTargets = make(map[string]bool)
}

// Test helper functions
func createTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = karov1alpha1.AddToScheme(scheme)

	return scheme
}

func createTestDeployment(name, namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": name},
				},
			},
		},
	}
}

func createTestRestartRule(name string, delayRestart *int32, targetName string) *karov1alpha1.RestartRule {
	return &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind: ConfigMapKind,
					Name: "test-config",
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: targetName,
				},
			},
			DelayRestart: delayRestart,
		},
	}
}

//nolint:cyclop,gocognit
func TestProcessRestartRules_DelayLogic(t *testing.T) {
	scheme := createTestScheme()

	tests := []struct {
		name                   string
		rules                  []*karov1alpha1.RestartRule
		expectedScheduleCalls  int
		expectedMaxDelayValues []time.Duration // Expected delays for each schedule call
		expectedRuleCounts     []int           // Expected number of rules per schedule call
		expectedHasDelayRules  []bool          // Expected hasDelayRule values
		description            string
	}{
		{
			name: "single rule without delay",
			rules: []*karov1alpha1.RestartRule{
				createTestRestartRule("rule1", nil, "app1"),
			},
			expectedScheduleCalls:  1,
			expectedMaxDelayValues: []time.Duration{0},
			expectedRuleCounts:     []int{1},
			expectedHasDelayRules:  []bool{false},
			description:            "Single rule without delay should execute immediately",
		},
		{
			name: "single rule with delay",
			rules: []*karov1alpha1.RestartRule{
				createTestRestartRule("rule1", int32Ptr(30), "app1"),
			},
			expectedScheduleCalls:  1,
			expectedMaxDelayValues: []time.Duration{30 * time.Second},
			expectedRuleCounts:     []int{1},
			expectedHasDelayRules:  []bool{true},
			description:            "Single rule with 30s delay should be scheduled with delay",
		},
		{
			name: "multiple rules same target - highest delay wins",
			rules: []*karov1alpha1.RestartRule{
				createTestRestartRule("rule1", int32Ptr(10), "app1"),
				createTestRestartRule("rule2", int32Ptr(30), "app1"),
				createTestRestartRule("rule3", int32Ptr(20), "app1"),
			},
			expectedScheduleCalls:  1,
			expectedMaxDelayValues: []time.Duration{30 * time.Second},
			expectedRuleCounts:     []int{3},
			expectedHasDelayRules:  []bool{true},
			description:            "Multiple rules for same target should use highest delay (30s)",
		},
		{
			name: "mixed delay and non-delay rules for same target",
			rules: []*karov1alpha1.RestartRule{
				createTestRestartRule("rule1", nil, "app1"),
				createTestRestartRule("rule2", int32Ptr(15), "app1"),
				createTestRestartRule("rule3", nil, "app1"),
			},
			expectedScheduleCalls:  1,
			expectedMaxDelayValues: []time.Duration{15 * time.Second},
			expectedRuleCounts:     []int{3},
			expectedHasDelayRules:  []bool{true},
			description:            "Mixed delay/non-delay rules should delay by highest delay (15s)",
		},
		{
			name: "multiple targets with different delays",
			rules: []*karov1alpha1.RestartRule{
				createTestRestartRule("rule1", int32Ptr(10), "app1"),
				createTestRestartRule("rule2", int32Ptr(20), "app2"),
			},
			expectedScheduleCalls:  2,
			expectedMaxDelayValues: []time.Duration{10 * time.Second, 20 * time.Second},
			expectedRuleCounts:     []int{1, 1},
			expectedHasDelayRules:  []bool{true, true},
			description:            "Multiple targets should each get their own schedule call with respective delays",
		},
		{
			name: "zero delay should be treated as immediate",
			rules: []*karov1alpha1.RestartRule{
				createTestRestartRule("rule1", int32Ptr(0), "app1"),
			},
			expectedScheduleCalls:  1,
			expectedMaxDelayValues: []time.Duration{0},
			expectedRuleCounts:     []int{1},
			expectedHasDelayRules:  []bool{true},
			description:            "Zero delay should still be handled by DelayedRestartManager but execute immediately",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create deployment objects that the rules target
			var deployments []client.Object
			targetNames := make(map[string]bool)
			for _, rule := range tt.rules {
				for _, target := range rule.Spec.Targets {
					if !targetNames[target.Name] {
						deployments = append(deployments, createTestDeployment(target.Name, "default"))
						targetNames[target.Name] = true
					}
				}
			}

			// Create fake client with deployments
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(deployments...).
				WithStatusSubresource(&karov1alpha1.RestartRule{}).
				Build()

			// Create mock delayed restart manager
			mockManager := NewMockDelayedRestartManager()

			// Create reconciler with mock manager
			reconciler := &BaseReconciler{
				Client:                fakeClient,
				RestartRuleStore:      store.NewMemoryRestartRuleStore(),
				DelayedRestartManager: mockManager,
			}

			ctx := context.Background()

			// Execute ProcessRestartRules
			err := reconciler.ProcessRestartRules(ctx, tt.rules, "test-config", ConfigMapKind)
			if err != nil {
				t.Fatalf("ProcessRestartRules() returned error: %v", err)
			}

			// Verify expected number of schedule calls
			scheduleArgs := mockManager.GetScheduleCallArgs()
			if len(scheduleArgs) != tt.expectedScheduleCalls {
				t.Errorf("Expected %d schedule calls, got %d", tt.expectedScheduleCalls, len(scheduleArgs))
			}

			// Verify each schedule call's parameters
			for i, expectedDelay := range tt.expectedMaxDelayValues {
				if i >= len(scheduleArgs) {
					t.Errorf("Missing schedule call %d", i)

					continue
				}

				args := scheduleArgs[i]
				if args.MaxDelay != expectedDelay {
					t.Errorf("Schedule call %d: expected delay %v, got %v", i, expectedDelay, args.MaxDelay)
				}
				if args.RulesCount != tt.expectedRuleCounts[i] {
					t.Errorf("Schedule call %d: expected %d rules, got %d", i, tt.expectedRuleCounts[i], args.RulesCount)
				}
				if args.HasDelayRule != tt.expectedHasDelayRules[i] {
					t.Errorf("Schedule call %d: expected hasDelayRule %v, got %v", i, tt.expectedHasDelayRules[i], args.HasDelayRule)
				}
			}
		})
	}
}

//nolint:cyclop
func TestProcessRestartRules_TargetGrouping(t *testing.T) {
	scheme := createTestScheme()

	// Create rules that target the same deployment but with different configurations
	rules := []*karov1alpha1.RestartRule{
		createTestRestartRule("rule1", int32Ptr(10), "app1"),
		createTestRestartRule("rule2", int32Ptr(5), "app1"),  // Same target as rule1
		createTestRestartRule("rule3", int32Ptr(15), "app2"), // Different target
	}

	deployment1 := createTestDeployment("app1", "default")
	deployment2 := createTestDeployment("app2", "default")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(deployment1, deployment2).
		WithStatusSubresource(&karov1alpha1.RestartRule{}).
		Build()

	mockManager := NewMockDelayedRestartManager()

	reconciler := &BaseReconciler{
		Client:                fakeClient,
		RestartRuleStore:      store.NewMemoryRestartRuleStore(),
		DelayedRestartManager: mockManager,
	}

	ctx := context.Background()
	err := reconciler.ProcessRestartRules(ctx, rules, "test-config", ConfigMapKind)
	if err != nil {
		t.Fatalf("ProcessRestartRules() returned error: %v", err)
	}

	// Should have 2 schedule calls - one for each unique target
	scheduleArgs := mockManager.GetScheduleCallArgs()
	if len(scheduleArgs) != 2 {
		t.Errorf("Expected 2 schedule calls, got %d", len(scheduleArgs))
	}

	// Verify that app1 target gets the highest delay from its rules (10s)
	// Verify that app2 target gets its delay (15s)
	foundApp1, foundApp2 := false, false
	for _, args := range scheduleArgs {
		// TODO: Use switch
		if args.TargetKey == "Deployment/default/app1" { //nolint:nestif,staticcheck
			foundApp1 = true
			if args.MaxDelay != 10*time.Second {
				t.Errorf("app1 target: expected delay 10s, got %v", args.MaxDelay)
			}
			if args.RulesCount != 2 {
				t.Errorf("app1 target: expected 2 rules, got %d", args.RulesCount)
			}
		} else if args.TargetKey == "Deployment/default/app2" {
			foundApp2 = true
			if args.MaxDelay != 15*time.Second {
				t.Errorf("app2 target: expected delay 15s, got %v", args.MaxDelay)
			}
			if args.RulesCount != 1 {
				t.Errorf("app2 target: expected 1 rule, got %d", args.RulesCount)
			}
		}
	}

	if !foundApp1 {
		t.Error("No schedule call found for app1 target")
	}
	if !foundApp2 {
		t.Error("No schedule call found for app2 target")
	}
}

func TestProcessRestartRules_DelayedRestartManagerNil(t *testing.T) {
	scheme := createTestScheme()

	rule := createTestRestartRule("rule1", int32Ptr(30), "app1")
	deployment := createTestDeployment("app1", "default")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(deployment).
		WithStatusSubresource(&karov1alpha1.RestartRule{}).
		Build()

	reconciler := &BaseReconciler{
		Client:                fakeClient,
		RestartRuleStore:      store.NewMemoryRestartRuleStore(),
		DelayedRestartManager: nil, // Intentionally nil to test fallback
	}

	ctx := context.Background()
	err := reconciler.ProcessRestartRules(ctx, []*karov1alpha1.RestartRule{rule}, "test-config", ConfigMapKind)

	// Should not return error even with nil DelayedRestartManager
	if err != nil {
		t.Errorf("ProcessRestartRules() with nil DelayedRestartManager returned error: %v", err)
	}

	// Verify that the deployment was updated (immediate restart occurred)
	var updatedDeployment appsv1.Deployment
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "app1", Namespace: "default"}, &updatedDeployment)
	if err != nil {
		t.Fatalf("Failed to get deployment: %v", err)
	}

	if updatedDeployment.Spec.Template.Annotations == nil ||
		updatedDeployment.Spec.Template.Annotations["karo.jeeatwork.com/restartedAt"] == "" {
		t.Error("Deployment should have been restarted immediately when DelayedRestartManager is nil")
	}
}

func TestProcessRestartRules_RestartFailure(t *testing.T) {
	scheme := createTestScheme()

	rule := createTestRestartRule("rule1", int32Ptr(5), "nonexistent-app")

	// Don't create the deployment - this will cause RestartDeployment to fail
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&karov1alpha1.RestartRule{}).
		Build()

	mockManager := NewMockDelayedRestartManager()

	reconciler := &BaseReconciler{
		Client:                fakeClient,
		RestartRuleStore:      store.NewMemoryRestartRuleStore(),
		DelayedRestartManager: mockManager,
	}

	ctx := context.Background()
	err := reconciler.ProcessRestartRules(ctx, []*karov1alpha1.RestartRule{rule}, "test-config", ConfigMapKind)

	// Should not return error from ProcessRestartRules itself
	if err != nil {
		t.Errorf("ProcessRestartRules() returned error: %v", err)
	}

	// Verify that ScheduleRestart was called
	scheduleArgs := mockManager.GetScheduleCallArgs()
	if len(scheduleArgs) != 1 {
		t.Fatalf("Expected 1 schedule call, got %d", len(scheduleArgs))
	}

	// Execute the restart function and verify it fails
	restartFunc := scheduleArgs[0].RestartFunc
	if restartFunc == nil {
		t.Fatal("RestartFunc should not be nil")
	}

	err = restartFunc()
	if err == nil {
		t.Error("RestartFunc should have failed for nonexistent deployment")
	}
}

//nolint:cyclop
func TestProcessRestartRules_StatusRecording(t *testing.T) {
	scheme := createTestScheme()

	rule := createTestRestartRule("rule1", int32Ptr(10), "app1")
	deployment := createTestDeployment("app1", "default")

	// Create fake client with the rule and deployment
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(rule, deployment).
		WithStatusSubresource(&karov1alpha1.RestartRule{}).
		Build()

	mockManager := NewMockDelayedRestartManager()

	reconciler := &BaseReconciler{
		Client:                fakeClient,
		RestartRuleStore:      store.NewMemoryRestartRuleStore(),
		DelayedRestartManager: mockManager,
	}

	ctx := context.Background()
	err := reconciler.ProcessRestartRules(ctx, []*karov1alpha1.RestartRule{rule}, "test-config", ConfigMapKind)
	if err != nil {
		t.Fatalf("ProcessRestartRules() returned error: %v", err)
	}

	// Execute the restart function (which should succeed and record status)
	scheduleArgs := mockManager.GetScheduleCallArgs()
	if len(scheduleArgs) != 1 {
		t.Fatalf("Expected 1 schedule call, got %d", len(scheduleArgs))
	}

	restartFunc := scheduleArgs[0].RestartFunc
	if restartFunc == nil {
		t.Fatal("RestartFunc should not be nil")
	}

	err = restartFunc()
	if err != nil {
		t.Errorf("RestartFunc failed: %v", err)
	}

	// Verify status was recorded in the rule
	var updatedRule karov1alpha1.RestartRule
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "rule1", Namespace: "default"}, &updatedRule)
	if err != nil {
		t.Fatalf("Failed to get updated rule: %v", err)
	}

	if len(updatedRule.Status.RestartHistory) != 1 {
		t.Errorf("Expected 1 restart event, got %d", len(updatedRule.Status.RestartHistory))
	}

	if len(updatedRule.Status.RestartHistory) > 0 {
		event := updatedRule.Status.RestartHistory[0]
		if event.Status != "Success" {
			t.Errorf("Expected restart status 'Success', got '%s'", event.Status)
		}
		if event.Target.Name != "app1" {
			t.Errorf("Expected target name 'app1', got '%s'", event.Target.Name)
		}
		if event.TriggerResource.Name != "test-config" {
			t.Errorf("Expected trigger resource 'test-config', got '%s'", event.TriggerResource.Name)
		}
		if event.TriggerResource.Kind != ConfigMapKind {
			t.Errorf("Expected trigger resource kind 'ConfigMap', got '%s'", event.TriggerResource.Kind)
		}
	}
}

func TestProcessRestartRules_EmptyRulesList(t *testing.T) {
	scheme := createTestScheme()

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	mockManager := NewMockDelayedRestartManager()

	reconciler := &BaseReconciler{
		Client:                fakeClient,
		RestartRuleStore:      store.NewMemoryRestartRuleStore(),
		DelayedRestartManager: mockManager,
	}

	ctx := context.Background()
	err := reconciler.ProcessRestartRules(ctx, []*karov1alpha1.RestartRule{}, "test-config", ConfigMapKind)

	if err != nil {
		t.Errorf("ProcessRestartRules() with empty rules returned error: %v", err)
	}

	// Should have no schedule calls
	scheduleArgs := mockManager.GetScheduleCallArgs()
	if len(scheduleArgs) != 0 {
		t.Errorf("Expected 0 schedule calls, got %d", len(scheduleArgs))
	}
}

func TestGetTargetKey(t *testing.T) {
	reconciler := &BaseReconciler{}

	rule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rule",
			Namespace: "default",
		},
	}

	tests := []struct {
		name     string
		target   karov1alpha1.TargetSpec
		expected string
	}{
		{
			name: "target with explicit namespace",
			target: karov1alpha1.TargetSpec{
				Kind:      "Deployment",
				Name:      "app1",
				Namespace: "custom",
			},
			expected: "Deployment/custom/app1",
		},
		{
			name: "target without namespace uses rule namespace",
			target: karov1alpha1.TargetSpec{
				Kind: "Deployment",
				Name: "app1",
			},
			expected: "Deployment/default/app1",
		},
		{
			name: "StatefulSet target",
			target: karov1alpha1.TargetSpec{
				Kind:      "StatefulSet",
				Name:      "stateful-app",
				Namespace: "production",
			},
			expected: "StatefulSet/production/stateful-app",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := reconciler.getTargetKey(tt.target, rule)
			if result != tt.expected {
				t.Errorf("getTargetKey() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestProcessRestartRules_MaxDelayCalculation(t *testing.T) {
	scheme := createTestScheme()

	tests := []struct {
		name        string
		delayValues []*int32
		expectedMax time.Duration
		description string
	}{
		{
			name:        "no delays",
			delayValues: []*int32{nil, nil},
			expectedMax: 0,
			description: "When no rules have delays, should use 0",
		},
		{
			name:        "single delay",
			delayValues: []*int32{int32Ptr(45)},
			expectedMax: 45 * time.Second,
			description: "Single delay should be used as-is",
		},
		{
			name:        "multiple delays - find maximum",
			delayValues: []*int32{int32Ptr(10), int32Ptr(60), int32Ptr(30)},
			expectedMax: 60 * time.Second,
			description: "Should select the highest delay value",
		},
		{
			name:        "mixed nil and delays",
			delayValues: []*int32{nil, int32Ptr(25), nil, int32Ptr(15)},
			expectedMax: 25 * time.Second,
			description: "Should ignore nil values and find max of non-nil",
		},
		{
			name:        "zero delay mixed with others",
			delayValues: []*int32{int32Ptr(0), int32Ptr(10), int32Ptr(5)},
			expectedMax: 10 * time.Second,
			description: "Zero delay should not prevent finding higher values",
		},
		{
			name:        "all zero delays",
			delayValues: []*int32{int32Ptr(0), int32Ptr(0)},
			expectedMax: 0,
			description: "All zero delays should result in 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create rules with the specified delay values
			var rules []*karov1alpha1.RestartRule
			for i, delay := range tt.delayValues {
				rule := createTestRestartRule(
					fmt.Sprintf("rule%d", i+1),
					delay,
					"app1", // All target the same app to ensure grouping
				)
				rules = append(rules, rule)
			}

			deployment := createTestDeployment("app1", "default")

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(deployment).
				WithStatusSubresource(&karov1alpha1.RestartRule{}).
				Build()

			mockManager := NewMockDelayedRestartManager()

			reconciler := &BaseReconciler{
				Client:                fakeClient,
				RestartRuleStore:      store.NewMemoryRestartRuleStore(),
				DelayedRestartManager: mockManager,
			}

			ctx := context.Background()
			err := reconciler.ProcessRestartRules(ctx, rules, "test-config", ConfigMapKind)
			if err != nil {
				t.Fatalf("ProcessRestartRules() returned error: %v", err)
			}

			// Verify the calculated max delay
			scheduleArgs := mockManager.GetScheduleCallArgs()
			if len(scheduleArgs) != 1 {
				t.Fatalf("Expected 1 schedule call, got %d", len(scheduleArgs))
			}

			if scheduleArgs[0].MaxDelay != tt.expectedMax {
				t.Errorf("Expected max delay %v, got %v", tt.expectedMax, scheduleArgs[0].MaxDelay)
			}
		})
	}
}
