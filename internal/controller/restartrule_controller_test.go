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
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"karo.jeeatwork.com/internal/store"
)

func TestRestartRuleReconciler_validateRule(t *testing.T) {
	tests := []struct {
		name    string
		rule    *karov1alpha1.RestartRule
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid rule with literal name",
			rule: &karov1alpha1.RestartRule{
				Spec: karov1alpha1.RestartRuleSpec{
					Changes: []karov1alpha1.ChangeSpec{
						{
							Kind: "ConfigMap",
							Name: "my-config",
						},
					},
					Targets: []karov1alpha1.TargetSpec{
						{
							Kind: "Deployment",
							Name: "my-app",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid rule with regex",
			rule: &karov1alpha1.RestartRule{
				Spec: karov1alpha1.RestartRuleSpec{
					Changes: []karov1alpha1.ChangeSpec{
						{
							Kind:    "ConfigMap",
							Name:    "my-config-.*",
							IsRegex: true,
						},
					},
					Targets: []karov1alpha1.TargetSpec{
						{
							Kind: "Deployment",
							Name: "my-app",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid regex pattern",
			rule: &karov1alpha1.RestartRule{
				Spec: karov1alpha1.RestartRuleSpec{
					Changes: []karov1alpha1.ChangeSpec{
						{
							Kind:    "ConfigMap",
							Name:    "[invalid-regex",
							IsRegex: true,
						},
					},
					Targets: []karov1alpha1.TargetSpec{
						{
							Kind: "Deployment",
							Name: "my-app",
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid regex pattern",
		},
		{
			name: "both name and selector specified in change",
			rule: &karov1alpha1.RestartRule{
				Spec: karov1alpha1.RestartRuleSpec{
					Changes: []karov1alpha1.ChangeSpec{
						{
							Kind: "ConfigMap",
							Name: "my-config",
							Selector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "test"},
							},
						},
					},
					Targets: []karov1alpha1.TargetSpec{
						{
							Kind: "Deployment",
							Name: "my-app",
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "cannot specify both name and selector",
		},
		{
			name: "both name and selector specified in target",
			rule: &karov1alpha1.RestartRule{
				Spec: karov1alpha1.RestartRuleSpec{
					Changes: []karov1alpha1.ChangeSpec{
						{
							Kind: "ConfigMap",
							Name: "my-config",
						},
					},
					Targets: []karov1alpha1.TargetSpec{
						{
							Kind: "Deployment",
							Name: "my-app",
							Selector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "test"},
							},
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "cannot specify both name and selector",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &RestartRuleReconciler{}
			err := r.validateRule(tt.rule)

			if tt.wantErr {
				if err == nil {
					t.Errorf("validateRule() expected error but got none")

					return
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("validateRule() error message %q does not contain %q", err.Error(), tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("validateRule() unexpected error: %v", err)
			}
		})
	}
}

func TestRestartRuleReconciler_Reconcile(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add clientgo scheme: %v", err)
	}
	if err := karov1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("Failed to add karo scheme: %v", err)
	}

	ctx := context.Background()

	tests := []struct {
		name          string
		rule          *karov1alpha1.RestartRule
		wantPhase     string
		wantReady     metav1.ConditionStatus
		wantValid     metav1.ConditionStatus
		expectInStore bool
	}{
		{
			name: "valid rule becomes active",
			rule: &karov1alpha1.RestartRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rule",
					Namespace: "default",
				},
				Spec: karov1alpha1.RestartRuleSpec{
					Changes: []karov1alpha1.ChangeSpec{
						{
							Kind: "ConfigMap",
							Name: "my-config",
						},
					},
					Targets: []karov1alpha1.TargetSpec{
						{
							Kind: "Deployment",
							Name: "my-app",
						},
					},
				},
			},
			wantPhase:     "Active",
			wantReady:     metav1.ConditionTrue,
			wantValid:     metav1.ConditionTrue,
			expectInStore: true,
		},
		{
			name: "invalid rule becomes invalid",
			rule: &karov1alpha1.RestartRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rule-invalid",
					Namespace: "default",
				},
				Spec: karov1alpha1.RestartRuleSpec{
					Changes: []karov1alpha1.ChangeSpec{
						{
							Kind:    "ConfigMap",
							Name:    "[invalid-regex",
							IsRegex: true,
						},
					},
					Targets: []karov1alpha1.TargetSpec{
						{
							Kind: "Deployment",
							Name: "my-app",
						},
					},
				},
			},
			wantPhase:     "Invalid",
			wantReady:     metav1.ConditionFalse,
			wantValid:     metav1.ConditionFalse,
			expectInStore: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler, memStore := createTestReconciler(scheme, tt.rule)
			req := createReconcileRequest(tt.rule)

			// Run reconcile and validate error expectations
			runReconcileAndValidateError(t, reconciler, ctx, req, tt.wantPhase)

			// Get and validate updated rule status
			getAndValidateUpdatedRule(t, reconciler.Client, ctx, req, tt)

			// Validate rule is in store as expected
			validateRuleInStore(t, memStore, ctx, tt)
		})
	}

	t.Run("rule not found", func(t *testing.T) {
		reconciler, memStore := createTestReconciler(scheme)
		rule := &karov1alpha1.RestartRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-rule",
				Namespace: "default",
			},
		}
		memStore.Add(ctx, rule)

		req := createReconcileRequest(rule)

		_, err := reconciler.Reconcile(ctx, req)
		if err != nil {
			t.Errorf("Reconcile() unexpected error for not found rule: %v", err)
		}

		rules := memStore.GetForConfigMap(ctx, corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-config",
				Namespace: "default",
			},
		}, store.OperationUpdate)
		if len(rules) != 0 {
			t.Errorf("Expected store to be empty, but found %d rules", len(rules))
		}
	})

	t.Run("get rule returns error", func(t *testing.T) {
		rule := &karov1alpha1.RestartRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-rule",
				Namespace: "default",
			},
		}
		reconciler, _ := createTestReconciler(scheme, rule)
		reconciler.Client = &fakeClientWithError{
			Client: reconciler.Client,
			getErr: errors.New("api error"),
		}

		req := createReconcileRequest(rule)

		_, err := reconciler.Reconcile(context.Background(), req)
		if err == nil {
			t.Error("Reconcile() expected error but got none")
		}
	})
}

func createTestReconciler(scheme *runtime.Scheme, initObjs ...client.Object) (*RestartRuleReconciler, *store.MemoryRestartRuleStore) {
	// Create fake client with the rule
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(initObjs...).
		WithStatusSubresource(&karov1alpha1.RestartRule{}).
		Build()

	// Create memory store
	memStore := store.NewMemoryRestartRuleStore()

	// Create reconciler
	return &RestartRuleReconciler{
		Client:           fakeClient,
		Scheme:           scheme,
		RestartRuleStore: memStore,
	}, memStore
}

func createReconcileRequest(rule *karov1alpha1.RestartRule) ctrl.Request {
	return ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      rule.Name,
			Namespace: rule.Namespace,
		},
	}
}

func runReconcileAndValidateError(t *testing.T, reconciler *RestartRuleReconciler, ctx context.Context, req ctrl.Request, wantPhase string) {
	_, err := reconciler.Reconcile(ctx, req)

	// For invalid rules, we expect an error to be returned
	if wantPhase == "Invalid" {
		if err == nil {
			t.Error("Reconcile() expected error for invalid rule but got none")
		}
	} else if err != nil {
		t.Errorf("Reconcile() unexpected error: %v", err)
	}
}

func getAndValidateUpdatedRule(t *testing.T, c client.Client, ctx context.Context, req ctrl.Request, tt struct {
	name          string
	rule          *karov1alpha1.RestartRule
	wantPhase     string
	wantReady     metav1.ConditionStatus
	wantValid     metav1.ConditionStatus
	expectInStore bool
}) {
	// Get updated rule
	var updatedRule karov1alpha1.RestartRule
	if err := c.Get(ctx, req.NamespacedName, &updatedRule); err != nil {
		t.Fatalf("Failed to get updated rule: %v", err)
	}

	// Check phase
	if updatedRule.Status.Phase != tt.wantPhase {
		t.Errorf("Expected phase %q, got %q", tt.wantPhase, updatedRule.Status.Phase)
	}

	// Check conditions
	validateStatusCondition(t, updatedRule.Status.Conditions, "Ready", tt.wantReady)
	validateStatusCondition(t, updatedRule.Status.Conditions, "Valid", tt.wantValid)
}

func validateStatusCondition(t *testing.T, conditions []metav1.Condition, conditionType string, expectedStatus metav1.ConditionStatus) {
	condition := meta.FindStatusCondition(conditions, conditionType)
	if condition == nil {
		t.Errorf("%s condition not found", conditionType)
	} else if condition.Status != expectedStatus {
		t.Errorf("Expected %s condition %q, got %q", conditionType, expectedStatus, condition.Status)
	}
}

func validateRuleInStore(t *testing.T, memStore store.RestartRuleStore, ctx context.Context, tt struct {
	name          string
	rule          *karov1alpha1.RestartRule
	wantPhase     string
	wantReady     metav1.ConditionStatus
	wantValid     metav1.ConditionStatus
	expectInStore bool
}) {
	// Check if rule is in store
	rules := memStore.GetForConfigMap(ctx, corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-config",
			Namespace: "default",
		},
	}, store.OperationUpdate)

	foundInStore := false
	for _, rule := range rules {
		if rule.Name == tt.rule.Name && rule.Namespace == tt.rule.Namespace {
			foundInStore = true

			break
		}
	}

	if foundInStore != tt.expectInStore {
		t.Errorf("Expected rule in store: %v, but found: %v", tt.expectInStore, foundInStore)
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && (s[0:len(substr)] == substr || contains(s[1:], substr))))
}

type fakeClientWithError struct {
	client.Client
	getErr error
}

func (f *fakeClientWithError) Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
	if f.getErr != nil {
		return f.getErr
	}
	return f.Client.Get(ctx, key, obj, opts...)
}
