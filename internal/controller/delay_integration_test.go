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
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"karo.jeeatwork.com/internal/store"
)

// TestDelayRestartIntegration tests the full flow from ConfigMap change to delayed restart
//
//nolint:cyclop,gocognit,maintidx
func TestDelayRestartIntegration(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = karov1alpha1.AddToScheme(scheme)

	ctx := context.Background()

	tests := []struct {
		name              string
		rules             []*karov1alpha1.RestartRule
		configMapName     string
		deploymentName    string
		expectedDelays    []time.Duration
		expectedSchedules int
		expectedTargets   []string
		description       string
	}{
		{
			name: "single rule with delay - ConfigMap change triggers delayed restart",
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-delay-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "ConfigMap",
								Name: "app-config",
							},
						},
						Targets: []karov1alpha1.TargetSpec{
							{
								Kind: "Deployment",
								Name: "test-app",
							},
						},
						DelayRestart: int32Ptr(60),
					},
				},
			},
			configMapName:     "app-config",
			deploymentName:    "test-app",
			expectedDelays:    []time.Duration{60 * time.Second},
			expectedSchedules: 1,
			expectedTargets:   []string{"Deployment/default/test-app"},
			description:       "Single rule should schedule delayed restart with 60s delay",
		},
		{
			name: "multiple rules same target - highest delay wins",
			rules: []*karov1alpha1.RestartRule{
				{
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
								Kind: "Deployment",
								Name: "shared-app",
							},
						},
						DelayRestart: int32Ptr(30),
					},
				},
				{
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
								Kind: "Deployment",
								Name: "shared-app",
							},
						},
						DelayRestart: int32Ptr(90),
					},
				},
			},
			configMapName:     "shared-config",
			deploymentName:    "shared-app",
			expectedDelays:    []time.Duration{90 * time.Second},
			expectedSchedules: 1,
			expectedTargets:   []string{"Deployment/default/shared-app"},
			description:       "Multiple rules targeting same app should use highest delay (90s)",
		},
		{
			name: "mixed delay and immediate rules - delay wins",
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "immediate-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "ConfigMap",
								Name: "mixed-config",
							},
						},
						Targets: []karov1alpha1.TargetSpec{
							{
								Kind: "Deployment",
								Name: "mixed-app",
							},
						},
						// No DelayRestart field - immediate restart
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "delayed-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "ConfigMap",
								Name: "mixed-config",
							},
						},
						Targets: []karov1alpha1.TargetSpec{
							{
								Kind: "Deployment",
								Name: "mixed-app",
							},
						},
						DelayRestart: int32Ptr(45),
					},
				},
			},
			configMapName:     "mixed-config",
			deploymentName:    "mixed-app",
			expectedDelays:    []time.Duration{45 * time.Second},
			expectedSchedules: 1,
			expectedTargets:   []string{"Deployment/default/mixed-app"},
			description:       "Mixed immediate and delayed rules should delay by 45s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create deployment
			deployment := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.deploymentName,
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": tt.deploymentName},
						},
					},
				},
			}

			// Create ConfigMap
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.configMapName,
					Namespace: "default",
				},
				Data: map[string]string{
					"key": "value",
				},
			}

			// Create objects for fake client
			var objs []runtime.Object
			objs = append(objs, deployment, configMap)
			for _, rule := range tt.rules {
				objs = append(objs, rule)
			}

			// Create fake client
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objs...).
				WithStatusSubresource(&karov1alpha1.RestartRule{}).
				Build()

			// Create store and add rules
			memStore := store.NewMemoryRestartRuleStore()
			for _, rule := range tt.rules {
				memStore.Add(ctx, rule)
			}

			// Create mock delayed restart manager
			mockManager := NewMockDelayedRestartManager()

			// Create ConfigMap controller
			configMapController := &ConfigMapReconciler{
				BaseReconciler: BaseReconciler{
					Client:                fakeClient,
					RestartRuleStore:      memStore,
					DelayedRestartManager: mockManager,
				},
			}

			// Simulate ConfigMap update by getting rules and processing them
			rules := memStore.GetForConfigMap(ctx, *configMap, store.OperationUpdate)
			if len(rules) != len(tt.rules) {
				t.Fatalf("Expected %d rules from store, got %d", len(tt.rules), len(rules))
			}

			// Process the restart rules (this simulates what happens in ConfigMap reconciler)
			err := configMapController.ProcessRestartRules(ctx, rules, configMap.Name, "ConfigMap")
			if err != nil {
				t.Fatalf("ProcessRestartRules failed: %v", err)
			}

			// Verify expected schedule calls
			scheduleArgs := mockManager.GetScheduleCallArgs()
			if len(scheduleArgs) != tt.expectedSchedules {
				t.Errorf("Expected %d schedule calls, got %d", tt.expectedSchedules, len(scheduleArgs))
			}

			// Verify delays and targets
			for i, expectedDelay := range tt.expectedDelays {
				if i >= len(scheduleArgs) {
					t.Errorf("Missing schedule call %d", i)

					continue
				}

				args := scheduleArgs[i]
				if args.MaxDelay != expectedDelay {
					t.Errorf("Schedule call %d: expected delay %v, got %v", i, expectedDelay, args.MaxDelay)
				}

				if i < len(tt.expectedTargets) && args.TargetKey != tt.expectedTargets[i] {
					t.Errorf("Schedule call %d: expected target %s, got %s", i, tt.expectedTargets[i], args.TargetKey)
				}
			}

			// Verify deployment gets restarted when restart function is called
			if len(scheduleArgs) > 0 {
				restartFunc := scheduleArgs[0].RestartFunc
				if restartFunc == nil {
					t.Fatal("RestartFunc should not be nil")
				}

				// Execute restart function
				err = restartFunc()
				if err != nil {
					t.Errorf("RestartFunc failed: %v", err)
				}

				// Verify deployment was updated
				var updatedDeployment appsv1.Deployment
				err = fakeClient.Get(ctx, types.NamespacedName{Name: tt.deploymentName, Namespace: "default"}, &updatedDeployment)
				if err != nil {
					t.Fatalf("Failed to get updated deployment: %v", err)
				}

				if updatedDeployment.Spec.Template.Annotations == nil ||
					updatedDeployment.Spec.Template.Annotations["karo.jeeatwork.com/restartedAt"] == "" {
					t.Error("Deployment should have restart annotation after restart function execution")
				}
			}
		})
	}
}

// TestSecretDelayRestartIntegration tests the full flow from Secret change to delayed restart
func TestSecretDelayRestartIntegration(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = karov1alpha1.AddToScheme(scheme)

	ctx := context.Background()

	// Create rule that watches Secrets with delay
	rule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret-delay-rule",
			Namespace: "default",
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind: "Secret",
					Name: "app-secret",
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: "secret-app",
				},
			},
			DelayRestart: int32Ptr(120),
		},
	}

	// Create deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret-app",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "secret-app"},
				},
			},
		},
	}

	// Create Secret
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app-secret",
			Namespace: "default",
		},
		Data: map[string][]byte{
			"password": []byte("secret-value"),
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(rule, deployment, secret).
		WithStatusSubresource(&karov1alpha1.RestartRule{}).
		Build()

	// Create store and add rule
	memStore := store.NewMemoryRestartRuleStore()
	memStore.Add(ctx, rule)

	// Create mock delayed restart manager
	mockManager := NewMockDelayedRestartManager()

	// Create Secret controller
	secretController := &SecretReconciler{
		BaseReconciler: BaseReconciler{
			Client:                fakeClient,
			RestartRuleStore:      memStore,
			DelayedRestartManager: mockManager,
		},
	}

	// Simulate Secret update by getting rules and processing them
	rules := memStore.GetForSecret(ctx, *secret, store.OperationUpdate)
	if len(rules) != 1 {
		t.Fatalf("Expected 1 rule from store, got %d", len(rules))
	}

	// Process the restart rules
	err := secretController.ProcessRestartRules(ctx, rules, secret.Name, "Secret")
	if err != nil {
		t.Fatalf("ProcessRestartRules failed: %v", err)
	}

	// Verify schedule was called with correct delay
	scheduleArgs := mockManager.GetScheduleCallArgs()
	if len(scheduleArgs) != 1 {
		t.Fatalf("Expected 1 schedule call, got %d", len(scheduleArgs))
	}

	if scheduleArgs[0].MaxDelay != 120*time.Second {
		t.Errorf("Expected delay 120s, got %v", scheduleArgs[0].MaxDelay)
	}

	if scheduleArgs[0].TargetKey != "Deployment/default/secret-app" {
		t.Errorf("Expected target 'Deployment/default/secret-app', got %s", scheduleArgs[0].TargetKey)
	}
}

// TestDelayRestartStatusIntegration tests that status events are recorded correctly during delayed restarts
//
//nolint:cyclop
func TestDelayRestartStatusIntegration(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = karov1alpha1.AddToScheme(scheme)

	ctx := context.Background()

	// Create rule with delay
	rule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "status-test-rule",
			Namespace: "default",
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind: "ConfigMap",
					Name: "status-config",
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: "status-app",
				},
			},
			DelayRestart: int32Ptr(15),
		},
	}

	// Create deployment and configmap
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "status-app",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "status-app"},
				},
			},
		},
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "status-config",
			Namespace: "default",
		},
		Data: map[string]string{
			"config": "value",
		},
	}

	// Create fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(rule, deployment, configMap).
		WithStatusSubresource(&karov1alpha1.RestartRule{}).
		Build()

	// Create store and add rule
	memStore := store.NewMemoryRestartRuleStore()
	memStore.Add(ctx, rule)

	// Create mock delayed restart manager
	mockManager := NewMockDelayedRestartManager()

	// Create base reconciler
	reconciler := &BaseReconciler{
		Client:                fakeClient,
		RestartRuleStore:      memStore,
		DelayedRestartManager: mockManager,
	}

	// Process restart rules
	rules := []*karov1alpha1.RestartRule{rule}
	err := reconciler.ProcessRestartRules(ctx, rules, configMap.Name, "ConfigMap")
	if err != nil {
		t.Fatalf("ProcessRestartRules failed: %v", err)
	}

	// Execute the restart function to trigger status recording
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

	// Verify status was recorded
	var updatedRule karov1alpha1.RestartRule
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "status-test-rule", Namespace: "default"}, &updatedRule)
	if err != nil {
		t.Fatalf("Failed to get updated rule: %v", err)
	}

	// Check restart history
	if len(updatedRule.Status.RestartHistory) != 1 {
		t.Errorf("Expected 1 restart event, got %d", len(updatedRule.Status.RestartHistory))
	}

	if len(updatedRule.Status.RestartHistory) > 0 { //nolint:nestif
		event := updatedRule.Status.RestartHistory[0]

		// Verify event details
		if event.Status != "Success" {
			t.Errorf("Expected status 'Success', got '%s'", event.Status)
		}

		if event.Target.Name != "status-app" {
			t.Errorf("Expected target 'status-app', got '%s'", event.Target.Name)
		}

		if event.Target.Kind != "Deployment" {
			t.Errorf("Expected target kind 'Deployment', got '%s'", event.Target.Kind)
		}

		if event.TriggerResource.Name != "status-config" {
			t.Errorf("Expected trigger resource 'status-config', got '%s'", event.TriggerResource.Name)
		}

		if event.TriggerResource.Kind != "ConfigMap" {
			t.Errorf("Expected trigger resource kind 'ConfigMap', got '%s'", event.TriggerResource.Kind)
		}

		if event.Timestamp.IsZero() {
			t.Error("Event timestamp should not be zero")
		}
	}

	// Check LastProcessedAt was updated
	if updatedRule.Status.LastProcessedAt == nil {
		t.Error("LastProcessedAt should be set")
	}
}
