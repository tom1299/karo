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
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"karo.jeeatwork.com/internal/store"
)

func TestProcessRestartRules_WithDelay(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = karov1alpha1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name            string
		rules           []*karov1alpha1.RestartRule
		resourceName    string
		resourceType    string
		expectDelayed   bool
		expectImmediate bool
	}{
		{
			name: "rule with delay schedules delayed restart",
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &metav1.Duration{Duration: 5 * time.Second},
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app1"},
						},
					},
				},
			},
			resourceName:    "config1",
			resourceType:    "ConfigMap",
			expectDelayed:   true,
			expectImmediate: false,
		},
		{
			name: "rule without delay executes immediate restart",
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
					Spec: karov1alpha1.RestartRuleSpec{
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app1"},
						},
					},
				},
			},
			resourceName:    "config1",
			resourceType:    "ConfigMap",
			expectDelayed:   false,
			expectImmediate: true,
		},
		{
			name: "mixed rules with and without delay uses delay",
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &metav1.Duration{Duration: 5 * time.Second},
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app1"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule2", Namespace: "default"},
					Spec: karov1alpha1.RestartRuleSpec{
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app2"},
						},
					},
				},
			},
			resourceName:    "config1",
			resourceType:    "ConfigMap",
			expectDelayed:   true,
			expectImmediate: false,
		},
		{
			name: "multiple rules with different delays uses max delay",
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &metav1.Duration{Duration: 5 * time.Second},
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app1"},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule2", Namespace: "default"},
					Spec: karov1alpha1.RestartRuleSpec{
						DelayRestart: &metav1.Duration{Duration: 10 * time.Second},
						Targets: []karov1alpha1.TargetSpec{
							{Kind: "Deployment", Name: "app2"},
						},
					},
				},
			},
			resourceName:    "config1",
			resourceType:    "ConfigMap",
			expectDelayed:   true,
			expectImmediate: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset global delay manager for each test
			globalDelayManager = &DelayManager{
				restarts: make(map[string]*DelayedRestart),
			}

			// Create deployments for testing
			deployment1 := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "app1", Namespace: "default"},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "app1"},
						},
					},
				},
			}
			deployment2 := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "app2", Namespace: "default"},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "app2"},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(deployment1, deployment2).
				Build()

			reconciler := &BaseReconciler{
				Client:           fakeClient,
				RestartRuleStore: &MockRestartRuleStore{},
			}

			ctx := context.Background()
			err := reconciler.ProcessRestartRules(ctx, tt.rules, tt.resourceName, tt.resourceType)

			if err != nil {
				t.Errorf("ProcessRestartRules() returned error: %v", err)
			}

			// Verify delayed restart behavior
			dm := GetDelayManager()
			hasDelayedRestarts := len(dm.restarts) > 0

			if tt.expectDelayed && !hasDelayedRestarts {
				t.Error("Expected delayed restarts but none were scheduled")
			}
			if !tt.expectDelayed && hasDelayedRestarts {
				t.Error("Expected no delayed restarts but some were scheduled")
			}

			// For immediate restart tests, verify deployment was updated
			if tt.expectImmediate {
				// Check that deployment has restart annotation
				var updatedDeployment appsv1.Deployment
				err := fakeClient.Get(ctx, client.ObjectKey{Name: "app1", Namespace: "default"}, &updatedDeployment)
				if err != nil {
					t.Errorf("Failed to get updated deployment: %v", err)
				}

				restartAnnotation := "karo.jeeatwork.com/restartedAt"
				if _, exists := updatedDeployment.Spec.Template.Annotations[restartAnnotation]; !exists {
					t.Error("Expected deployment to have restart annotation for immediate restart")
				}
			}
		})
	}
}

func TestProcessRestartRules_DelayCalculation(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = karov1alpha1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	// Reset global delay manager
	globalDelayManager = &DelayManager{
		restarts: make(map[string]*DelayedRestart),
	}

	rules := []*karov1alpha1.RestartRule{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
			Spec: karov1alpha1.RestartRuleSpec{
				DelayRestart: &metav1.Duration{Duration: 5 * time.Second},
				Targets: []karov1alpha1.TargetSpec{
					{Kind: "Deployment", Name: "app1"},
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "rule2", Namespace: "default"},
			Spec: karov1alpha1.RestartRuleSpec{
				DelayRestart: &metav1.Duration{Duration: 10 * time.Second},
				Targets: []karov1alpha1.TargetSpec{
					{Kind: "Deployment", Name: "app1"}, // Same target
				},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "rule3", Namespace: "default"},
			Spec: karov1alpha1.RestartRuleSpec{
				DelayRestart: &metav1.Duration{Duration: 3 * time.Second},
				Targets: []karov1alpha1.TargetSpec{
					{Kind: "Deployment", Name: "app1"}, // Same target
				},
			},
		},
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app1", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "app1"},
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(deployment).
		Build()

	reconciler := &BaseReconciler{
		Client:           fakeClient,
		RestartRuleStore: &MockRestartRuleStore{},
	}

	ctx := context.Background()
	err := reconciler.ProcessRestartRules(ctx, rules, "config1", "ConfigMap")

	if err != nil {
		t.Errorf("ProcessRestartRules() returned error: %v", err)
	}

	// Verify that only one delayed restart was scheduled with the max delay
	dm := GetDelayManager()
	if len(dm.restarts) != 1 {
		t.Errorf("Expected 1 delayed restart, got %d", len(dm.restarts))
		return
	}

	// The delay should be the maximum (10 seconds)
	var delayedRestart *DelayedRestart
	for _, restart := range dm.restarts {
		delayedRestart = restart
		break
	}

	expectedMaxDelay := 10 * time.Second
	if delayedRestart.MaxDelay != expectedMaxDelay {
		t.Errorf("Expected max delay %v, got %v", expectedMaxDelay, delayedRestart.MaxDelay)
	}
}

// MockRestartRuleStore implements the RestartRuleStore interface for testing
type MockRestartRuleStore struct{}

func (m *MockRestartRuleStore) Add(ctx context.Context, rule *karov1alpha1.RestartRule) {}

func (m *MockRestartRuleStore) Remove(ctx context.Context, namespace, name string) {}

func (m *MockRestartRuleStore) GetForSecret(ctx context.Context, secret corev1.Secret, operation store.OperationType) []*karov1alpha1.RestartRule {
	return nil
}

func (m *MockRestartRuleStore) GetForConfigMap(ctx context.Context, configMap corev1.ConfigMap, operation store.OperationType) []*karov1alpha1.RestartRule {
	return nil
}
