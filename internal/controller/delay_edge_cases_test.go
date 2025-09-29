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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"karo.jeeatwork.com/internal/store"
)

// TestProcessRestartRules_EdgeCases tests various edge cases for the delay restart integration
//
//nolint:gocognit,cyclop,maintidx
func TestProcessRestartRules_EdgeCases(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = karov1alpha1.AddToScheme(scheme)

	ctx := context.Background()

	t.Run("context cancellation during processing", func(t *testing.T) {
		rule := createTestRestartRule("rule1", int32Ptr(30), "app1")
		deployment := createTestDeployment("app1", "default")

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(deployment).
			WithStatusSubresource(&karov1alpha1.RestartRule{}).
			Build()

		// Create context that is already cancelled
		cancelledCtx, cancel := context.WithCancel(ctx)
		cancel()

		mockManager := NewMockDelayedRestartManager()

		reconciler := &BaseReconciler{
			Client:                fakeClient,
			RestartRuleStore:      store.NewMemoryRestartRuleStore(),
			DelayedRestartManager: mockManager,
		}

		err := reconciler.ProcessRestartRules(cancelledCtx, []*karov1alpha1.RestartRule{rule}, "test-config", "ConfigMap")

		// Should not return error even with cancelled context
		if err != nil {
			t.Errorf("ProcessRestartRules() with cancelled context returned error: %v", err)
		}

		// Should still schedule restart (DelayedRestartManager handles cancellation)
		scheduleArgs := mockManager.GetScheduleCallArgs()
		if len(scheduleArgs) != 1 {
			t.Errorf("Expected 1 schedule call even with cancelled context, got %d", len(scheduleArgs))
		}
	})

	t.Run("delayedRestartManager returns error during execution", func(t *testing.T) {
		rule := createTestRestartRule("rule1", int32Ptr(5), "nonexistent-app")

		// Don't create the deployment - this will cause RestartDeployment to fail
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(&karov1alpha1.RestartRule{}).
			Build()

		mockManager := NewMockDelayedRestartManager()
		mockManager.SetShouldFail(true) // Make the mock not execute the restart func

		reconciler := &BaseReconciler{
			Client:                fakeClient,
			RestartRuleStore:      store.NewMemoryRestartRuleStore(),
			DelayedRestartManager: mockManager,
		}

		err := reconciler.ProcessRestartRules(ctx, []*karov1alpha1.RestartRule{rule}, "test-config", "ConfigMap")

		// ProcessRestartRules should not return error
		if err != nil {
			t.Errorf("ProcessRestartRules() returned unexpected error: %v", err)
		}

		// Should still have called ScheduleRestart
		scheduleArgs := mockManager.GetScheduleCallArgs()
		if len(scheduleArgs) != 1 {
			t.Errorf("Expected 1 schedule call, got %d", len(scheduleArgs))
		}
	})

	t.Run("multiple identical rules for same target - no duplicate schedules", func(t *testing.T) {
		// Create three identical rules (same target, same delay)
		rules := []*karov1alpha1.RestartRule{
			createTestRestartRule("rule1", int32Ptr(20), "app1"),
			createTestRestartRule("rule2", int32Ptr(20), "app1"),
			createTestRestartRule("rule3", int32Ptr(20), "app1"),
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

		err := reconciler.ProcessRestartRules(ctx, rules, "test-config", "ConfigMap")
		if err != nil {
			t.Errorf("ProcessRestartRules() returned error: %v", err)
		}

		// Should only have one schedule call since all rules target the same deployment
		scheduleArgs := mockManager.GetScheduleCallArgs()
		if len(scheduleArgs) != 1 {
			t.Errorf("Expected 1 schedule call for identical targets, got %d", len(scheduleArgs))
		}

		if len(scheduleArgs) > 0 {
			args := scheduleArgs[0]
			if args.RulesCount != 3 {
				t.Errorf("Expected 3 rules in schedule call, got %d", args.RulesCount)
			}
			if args.MaxDelay != 20*time.Second {
				t.Errorf("Expected delay 20s, got %v", args.MaxDelay)
			}
		}
	})

	t.Run("rules with different target namespaces", func(t *testing.T) {
		// Create rules targeting deployments in different namespaces
		rule1 := &karov1alpha1.RestartRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rule1",
				Namespace: "default",
			},
			Spec: karov1alpha1.RestartRuleSpec{
				Changes: []karov1alpha1.ChangeSpec{
					{
						Kind: "ConfigMap",
						Name: "shared-config",
					},
				},
				Targets: []karov1alpha1.TargetSpec{
					{
						Kind:      "Deployment",
						Name:      "app1",
						Namespace: "namespace1",
					},
				},
				DelayRestart: int32Ptr(10),
			},
		}

		rule2 := &karov1alpha1.RestartRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rule2",
				Namespace: "default",
			},
			Spec: karov1alpha1.RestartRuleSpec{
				Changes: []karov1alpha1.ChangeSpec{
					{
						Kind: "ConfigMap",
						Name: "shared-config",
					},
				},
				Targets: []karov1alpha1.TargetSpec{
					{
						Kind:      "Deployment",
						Name:      "app1", // Same name but different namespace
						Namespace: "namespace2",
					},
				},
				DelayRestart: int32Ptr(15),
			},
		}

		deployment1 := createTestDeployment("app1", "namespace1")
		deployment2 := createTestDeployment("app1", "namespace2")

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

		err := reconciler.ProcessRestartRules(ctx, []*karov1alpha1.RestartRule{rule1, rule2}, "shared-config", "ConfigMap")
		if err != nil {
			t.Errorf("ProcessRestartRules() returned error: %v", err)
		}

		// Should have two schedule calls - one for each namespace
		scheduleArgs := mockManager.GetScheduleCallArgs()
		if len(scheduleArgs) != 2 {
			t.Errorf("Expected 2 schedule calls for different namespaces, got %d", len(scheduleArgs))
		}

		// Verify target keys are different
		if len(scheduleArgs) == 2 {
			targetKeys := []string{scheduleArgs[0].TargetKey, scheduleArgs[1].TargetKey}
			expectedKeys := []string{"Deployment/namespace1/app1", "Deployment/namespace2/app1"}

			foundNs1, foundNs2 := false, false
			for _, key := range targetKeys {
				if key == expectedKeys[0] {
					foundNs1 = true
				}
				if key == expectedKeys[1] {
					foundNs2 = true
				}
			}

			if !foundNs1 || !foundNs2 {
				t.Errorf("Expected target keys %v, got %v", expectedKeys, targetKeys)
			}
		}
	})

	t.Run("empty target name with namespace fallback", func(t *testing.T) {
		rule := &karov1alpha1.RestartRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "rule1",
				Namespace: "test-namespace",
			},
			Spec: karov1alpha1.RestartRuleSpec{
				Changes: []karov1alpha1.ChangeSpec{
					{
						Kind: "ConfigMap",
						Name: "test-config",
					},
				},
				Targets: []karov1alpha1.TargetSpec{
					{
						Kind: "Deployment",
						Name: "app1",
						// No namespace specified - should use rule namespace
					},
				},
				DelayRestart: int32Ptr(25),
			},
		}

		deployment := createTestDeployment("app1", "test-namespace")

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

		err := reconciler.ProcessRestartRules(ctx, []*karov1alpha1.RestartRule{rule}, "test-config", "ConfigMap")
		if err != nil {
			t.Errorf("ProcessRestartRules() returned error: %v", err)
		}

		scheduleArgs := mockManager.GetScheduleCallArgs()
		if len(scheduleArgs) != 1 {
			t.Errorf("Expected 1 schedule call, got %d", len(scheduleArgs))
		}

		if len(scheduleArgs) > 0 && scheduleArgs[0].TargetKey != "Deployment/test-namespace/app1" {
			t.Errorf("Expected target key 'Deployment/test-namespace/app1', got '%s'", scheduleArgs[0].TargetKey)
		}
	})

	t.Run("very large delay values", func(t *testing.T) {
		rule := createTestRestartRule("rule1", int32Ptr(3600), "app1") // Maximum allowed delay
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

		err := reconciler.ProcessRestartRules(ctx, []*karov1alpha1.RestartRule{rule}, "test-config", "ConfigMap")
		if err != nil {
			t.Errorf("ProcessRestartRules() returned error: %v", err)
		}

		scheduleArgs := mockManager.GetScheduleCallArgs()
		if len(scheduleArgs) != 1 {
			t.Errorf("Expected 1 schedule call, got %d", len(scheduleArgs))
		}

		if len(scheduleArgs) > 0 && scheduleArgs[0].MaxDelay != 3600*time.Second {
			t.Errorf("Expected delay 3600s, got %v", scheduleArgs[0].MaxDelay)
		}
	})
}

// TestProcessRestartRules_ConcurrentExecution tests concurrent rule processing
func TestProcessRestartRules_ConcurrentExecution(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = karov1alpha1.AddToScheme(scheme)

	ctx := context.Background()

	// Create multiple deployments and rules
	var deployments []runtime.Object
	var rules []*karov1alpha1.RestartRule

	for i := range 10 {
		deployName := fmt.Sprintf("app%d", i)
		ruleName := fmt.Sprintf("rule%d", i)

		deployment := createTestDeployment(deployName, "default")
		deployments = append(deployments, deployment)

		// TODO: How to do type safe int32 conversion?
		delayValue := int32(i * 5) //nolint:gosec // disable G115
		rule := createTestRestartRule(ruleName, int32Ptr(delayValue), deployName)
		rules = append(rules, rule)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(deployments...).
		WithStatusSubresource(&karov1alpha1.RestartRule{}).
		Build()

	// Thread-safe mock manager
	mockManager := NewMockDelayedRestartManager()

	reconciler := &BaseReconciler{
		Client:                fakeClient,
		RestartRuleStore:      store.NewMemoryRestartRuleStore(),
		DelayedRestartManager: mockManager,
	}

	// Run ProcessRestartRules concurrently
	var wg sync.WaitGroup
	errChan := make(chan error, 5)

	for i := range 5 {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			err := reconciler.ProcessRestartRules(ctx, rules, fmt.Sprintf("config-%d", goroutineID), "ConfigMap")
			if err != nil {
				errChan <- err
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	for err := range errChan {
		t.Errorf("Concurrent ProcessRestartRules() returned error: %v", err)
	}

	// Should have many schedule calls (5 goroutines * 10 rules each = 50 calls)
	scheduleArgs := mockManager.GetScheduleCallArgs()
	if len(scheduleArgs) != 50 {
		t.Errorf("Expected 50 schedule calls from concurrent execution, got %d", len(scheduleArgs))
	}
}

// TestDelayedRestartManager_StoppedBehavior tests behavior when manager is stopped
func TestDelayedRestartManager_StoppedBehavior(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = karov1alpha1.AddToScheme(scheme)

	ctx := context.Background()

	rule := createTestRestartRule("rule1", int32Ptr(10), "app1")
	deployment := createTestDeployment("app1", "default")

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(deployment).
		WithStatusSubresource(&karov1alpha1.RestartRule{}).
		Build()

	mockManager := NewMockDelayedRestartManager()
	mockManager.Stop() // Stop the manager before using it

	reconciler := &BaseReconciler{
		Client:                fakeClient,
		RestartRuleStore:      store.NewMemoryRestartRuleStore(),
		DelayedRestartManager: mockManager,
	}

	err := reconciler.ProcessRestartRules(ctx, []*karov1alpha1.RestartRule{rule}, "test-config", "ConfigMap")
	if err != nil {
		t.Errorf("ProcessRestartRules() with stopped manager returned error: %v", err)
	}

	// Should have no schedule calls since manager is stopped
	scheduleArgs := mockManager.GetScheduleCallArgs()
	if len(scheduleArgs) != 0 {
		t.Errorf("Expected 0 schedule calls with stopped manager, got %d", len(scheduleArgs))
	}
}

// TestProcessRestartRules_RestartFunctionFailures tests various restart function failure scenarios
func TestProcessRestartRules_RestartFunctionFailures(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = karov1alpha1.AddToScheme(scheme)

	ctx := context.Background()

	tests := []struct {
		name        string
		setup       func() (*karov1alpha1.RestartRule, []runtime.Object)
		expectError bool
		description string
	}{
		{
			name: "deployment not found",
			setup: func() (*karov1alpha1.RestartRule, []runtime.Object) {
				rule := createTestRestartRule("rule1", int32Ptr(5), "nonexistent-app")

				return rule, []runtime.Object{rule} // No deployment created
			},
			expectError: true,
			description: "RestartFunc should fail when deployment doesn't exist",
		},
		{
			name: "deployment in different namespace",
			setup: func() (*karov1alpha1.RestartRule, []runtime.Object) {
				rule := createTestRestartRule("rule1", int32Ptr(5), "app1")
				deployment := createTestDeployment("app1", "wrong-namespace")

				return rule, []runtime.Object{rule, deployment}
			},
			expectError: true,
			description: "RestartFunc should fail when deployment is in wrong namespace",
		},
		{
			name: "successful restart",
			setup: func() (*karov1alpha1.RestartRule, []runtime.Object) {
				rule := createTestRestartRule("rule1", int32Ptr(5), "app1")
				deployment := createTestDeployment("app1", "default")

				return rule, []runtime.Object{rule, deployment}
			},
			expectError: false,
			description: "RestartFunc should succeed when deployment exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule, objects := tt.setup()

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				WithStatusSubresource(&karov1alpha1.RestartRule{}).
				Build()

			mockManager := NewMockDelayedRestartManager()

			reconciler := &BaseReconciler{
				Client:                fakeClient,
				RestartRuleStore:      store.NewMemoryRestartRuleStore(),
				DelayedRestartManager: mockManager,
			}

			err := reconciler.ProcessRestartRules(ctx, []*karov1alpha1.RestartRule{rule}, "test-config", "ConfigMap")
			if err != nil {
				t.Errorf("ProcessRestartRules() returned unexpected error: %v", err)
			}

			// Get and execute the restart function
			scheduleArgs := mockManager.GetScheduleCallArgs()
			if len(scheduleArgs) != 1 {
				t.Fatalf("Expected 1 schedule call, got %d", len(scheduleArgs))
			}

			restartFunc := scheduleArgs[0].RestartFunc
			if restartFunc == nil {
				t.Fatal("RestartFunc should not be nil")
			}

			err = restartFunc()

			if tt.expectError && err == nil {
				t.Errorf("Expected RestartFunc to fail but it succeeded")
			} else if !tt.expectError && err != nil {
				t.Errorf("Expected RestartFunc to succeed but it failed: %v", err)
			}
		})
	}
}

// TestProcessRestartRules_StatusRecordingFailure tests status recording failures
func TestProcessRestartRules_StatusRecordingFailure(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = karov1alpha1.AddToScheme(scheme)

	ctx := context.Background()

	rule := createTestRestartRule("rule1", int32Ptr(5), "app1")
	deployment := createTestDeployment("app1", "default")

	// Create a fake client that fails status updates
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

	// Remove the rule from client to cause status update failure
	err := fakeClient.Delete(ctx, rule)
	if err != nil {
		t.Fatalf("Failed to delete rule from client: %v", err)
	}

	err = reconciler.ProcessRestartRules(ctx, []*karov1alpha1.RestartRule{rule}, "test-config", "ConfigMap")
	if err != nil {
		t.Errorf("ProcessRestartRules() returned error: %v", err)
	}

	// Execute the restart function (should still work even if status recording fails)
	scheduleArgs := mockManager.GetScheduleCallArgs()
	if len(scheduleArgs) != 1 {
		t.Fatalf("Expected 1 schedule call, got %d", len(scheduleArgs))
	}

	restartFunc := scheduleArgs[0].RestartFunc
	if restartFunc == nil {
		t.Fatal("RestartFunc should not be nil")
	}

	err = restartFunc()
	// Should not fail the restart operation even if status recording fails
	if err != nil {
		t.Errorf("RestartFunc failed: %v", err)
	}

	// Verify deployment was still restarted
	var updatedDeployment appsv1.Deployment
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "app1", Namespace: "default"}, &updatedDeployment)
	if err != nil {
		t.Fatalf("Failed to get deployment: %v", err)
	}

	if updatedDeployment.Spec.Template.Annotations == nil ||
		updatedDeployment.Spec.Template.Annotations["karo.jeeatwork.com/restartedAt"] == "" {
		t.Error("Deployment should have been restarted despite status recording failure")
	}
}
