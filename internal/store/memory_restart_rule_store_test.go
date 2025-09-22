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

package store

import (
	"context"
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

func TestNewMemoryRestartRuleStore(t *testing.T) {
	t.Parallel()

	store := NewMemoryRestartRuleStore()
	if store == nil {
		t.Fatal("NewMemoryRestartRuleStore() returned nil")
	}
	if store.rules == nil {
		t.Fatal("NewMemoryRestartRuleStore() created store with nil rules map")
	}
	if len(store.rules) != 0 {
		t.Errorf("NewMemoryRestartRuleStore() created store with non-empty rules map, got %d rules", len(store.rules))
	}
}

func TestMemoryRestartRuleStore_Add(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rule *karov1alpha1.RestartRule
	}{
		{
			name: "add new restart rule",
			rule: &karov1alpha1.RestartRule{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rule",
					Namespace: "default",
				},
				Spec: karov1alpha1.RestartRuleSpec{
					Changes: []karov1alpha1.ChangeSpec{
						{
							Kind: "Secret",
							Name: "test-secret",
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := NewMemoryRestartRuleStore()
			ctx := context.Background()

			store.Add(ctx, tt.rule)

			key := tt.rule.Namespace + "/" + tt.rule.Name
			if !reflect.DeepEqual(store.rules[key], tt.rule) {
				t.Errorf("Add() failed to store rule correctly, got %v, want %v", store.rules[key], tt.rule)
			}
		})
	}
}

func TestMemoryRestartRuleStore_Add_Update(t *testing.T) {
	t.Parallel()

	store := NewMemoryRestartRuleStore()
	ctx := context.Background()

	rule1 := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rule",
			Namespace: "default",
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind: "Secret",
					Name: "test-secret",
				},
			},
		},
	}

	rule2 := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rule",
			Namespace: "default",
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind: "ConfigMap",
					Name: "test-configmap",
				},
			},
		},
	}

	store.Add(ctx, rule1)
	store.Add(ctx, rule2)

	key := "default/test-rule"
	if !reflect.DeepEqual(store.rules[key], rule2) {
		t.Errorf("Add() failed to update existing rule, got %v, want %v", store.rules[key], rule2)
	}
}

func TestMemoryRestartRuleStore_Remove(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		ruleName  string
		setup     func(*MemoryRestartRuleStore)
		wantNil   bool
	}{
		{
			name:      "remove existing rule",
			namespace: "default",
			ruleName:  "test-rule",
			setup: func(store *MemoryRestartRuleStore) {
				rule := &karov1alpha1.RestartRule{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
				}
				store.Add(context.Background(), rule)
			},
			wantNil: true,
		},
		{
			name:      "remove non-existent rule",
			namespace: "default",
			ruleName:  "non-existent",
			setup:     func(store *MemoryRestartRuleStore) {},
			wantNil:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := NewMemoryRestartRuleStore()
			ctx := context.Background()
			tt.setup(store)

			store.Remove(ctx, tt.namespace, tt.ruleName)

			key := tt.namespace + "/" + tt.ruleName
			if tt.wantNil && store.rules[key] != nil {
				t.Errorf("Remove() failed to remove rule, got %v, want nil", store.rules[key])
			}
		})
	}
}

func TestMemoryRestartRuleStore_GetForSecret(t *testing.T) {
	tests := []struct {
		name      string
		secret    v1.Secret
		operation OperationType
		rules     []*karov1alpha1.RestartRule
		expected  int
	}{
		{
			name: "match secret by name with Update operation",
			secret: v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
			},
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "Secret",
								Name: "test-secret",
								// Empty ChangeType defaults to Update only
							},
						},
					},
				},
			},
			expected: 1,
		},
		{
			name: "no match with empty ChangeType for Create operation",
			secret: v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
			},
			operation: OperationCreate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "Secret",
								Name: "test-secret",
								// Empty ChangeType only matches Update
							},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name: "match secret by label selector with Update operation",
			secret: v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
					Labels: map[string]string{
						"app": "test-app",
						"env": "production",
					},
				},
			},
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "Secret",
								Selector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"app": "test-app",
									},
								},
								// Empty ChangeType defaults to Update only
							},
						},
					},
				},
			},
			expected: 1,
		},
		{
			name: "no match for different kind",
			secret: v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
			},
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "ConfigMap",
								Name: "test-secret",
							},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name: "no match for different name",
			secret: v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
			},
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "Secret",
								Name: "different-secret",
							},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name: "no match for different label selector",
			secret: v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
					Labels: map[string]string{
						"app": "test-app",
					},
				},
			},
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "Secret",
								Selector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"app": "different-app",
									},
								},
							},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name: "empty store",
			secret: v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-secret",
					Namespace: "default",
				},
			},
			operation: OperationUpdate,
			rules:     []*karov1alpha1.RestartRule{},
			expected:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := NewMemoryRestartRuleStore()
			ctx := context.Background()

			for _, rule := range tt.rules {
				store.Add(ctx, rule)
			}

			result := store.GetForSecret(ctx, tt.secret, tt.operation)
			if len(result) != tt.expected {
				t.Errorf("GetForSecret() returned %d rules, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestMemoryRestartRuleStore_GetForConfigMap(t *testing.T) {
	tests := []struct {
		name      string
		configMap v1.ConfigMap
		operation OperationType
		rules     []*karov1alpha1.RestartRule
		expected  int
	}{
		{
			name: "match configmap by name with Update operation",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-configmap",
					Namespace: "default",
				},
			},
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "ConfigMap",
								Name: "test-configmap",
								// Empty ChangeType defaults to Update only
							},
						},
					},
				},
			},
			expected: 1,
		},
		{
			name: "no match with empty ChangeType for Create operation",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-configmap",
					Namespace: "default",
				},
			},
			operation: OperationCreate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "ConfigMap",
								Name: "test-configmap",
								// Empty ChangeType only matches Update
							},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name: "match configmap by label selector with Update operation",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-configmap",
					Namespace: "default",
					Labels: map[string]string{
						"app": "test-app",
						"env": "production",
					},
				},
			},
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "ConfigMap",
								Selector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"env": "production",
									},
								},
								// Empty ChangeType defaults to Update only
							},
						},
					},
				},
			},
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := NewMemoryRestartRuleStore()
			ctx := context.Background()

			for _, rule := range tt.rules {
				store.Add(ctx, rule)
			}

			result := store.GetForConfigMap(ctx, tt.configMap, tt.operation)
			if len(result) != tt.expected {
				t.Errorf("GetForConfigMap() returned %d rules, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestMemoryRestartRuleStore_GetForKind(t *testing.T) {
	tests := []struct {
		name      string
		meta      metav1.ObjectMeta
		kind      string
		operation OperationType
		rules     []*karov1alpha1.RestartRule
		expected  int
	}{
		{
			name: "multiple matching changes in same rule should return rule once",
			meta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "default",
				Labels: map[string]string{
					"app": "test-app",
				},
			},
			kind:      "Secret",
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "Secret",
								Name: "test-resource",
							},
							{
								Kind: "Secret",
								Selector: &metav1.LabelSelector{
									MatchLabels: map[string]string{
										"app": "test-app",
									},
								},
							},
						},
					},
				},
			},
			expected: 1, // Should only return the rule once due to break statement
		},
		{
			name: "invalid label selector should not match",
			meta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "default",
			},
			kind:      "Secret",
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "Secret",
								Selector: &metav1.LabelSelector{
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{
											Key:      "invalid",
											Operator: "InvalidOperator",
											Values:   []string{"value"},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: 0,
		},
		{
			name: "multiple rules matching same resource",
			meta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "default",
			},
			kind:      "Secret",
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule-1",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "Secret",
								Name: "test-resource",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule-2",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "Secret",
								Name: "test-resource",
							},
						},
					},
				},
			},
			expected: 2,
		},
		{
			name: "empty name and selector should not match",
			meta: metav1.ObjectMeta{
				Name:      "test-resource",
				Namespace: "default",
			},
			kind:      "Secret",
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "Secret",
								// No name or selector specified
							},
						},
					},
				},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := NewMemoryRestartRuleStore()
			ctx := context.Background()

			for _, rule := range tt.rules {
				store.Add(ctx, rule)
			}

			result := store.GetForKind(ctx, tt.meta, tt.kind, tt.operation)
			if len(result) != tt.expected {
				t.Errorf("GetForKind() returned %d rules, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestMemoryRestartRuleStore_SkipNilRules(t *testing.T) {
	store := NewMemoryRestartRuleStore()
	ctx := context.Background()

	rule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rule",
			Namespace: "default",
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind: "Secret",
					Name: "test-secret",
				},
			},
		},
	}

	store.Add(ctx, rule)
	store.Remove(ctx, "default", "test-rule") // This sets the rule to nil

	secret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
	}

	rules := store.GetForSecret(ctx, secret, OperationUpdate)
	if len(rules) != 0 {
		t.Errorf("GetForSecret() should skip nil rules, got %d rules, want 0", len(rules))
	}
}

func TestMemoryRestartRuleStore_EmptyChanges(t *testing.T) {
	store := NewMemoryRestartRuleStore()
	ctx := context.Background()

	rule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rule",
			Namespace: "default",
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{},
		},
	}

	store.Add(ctx, rule)

	secret := v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "default",
		},
	}

	rules := store.GetForSecret(ctx, secret, OperationUpdate)
	if len(rules) != 0 {
		t.Errorf("GetForSecret() should handle empty changes slice, got %d rules, want 0", len(rules))
	}
}

func TestMemoryRestartRuleStore_matchesNameOrSelector_Regex(t *testing.T) {
	store := NewMemoryRestartRuleStore()

	tests := []struct {
		name     string
		change   karov1alpha1.ChangeSpec
		meta     metav1.ObjectMeta
		expected bool
	}{
		{
			name: "exact name match (backward compatibility)",
			change: karov1alpha1.ChangeSpec{
				Kind: "ConfigMap",
				Name: "nginx-config",
			},
			meta: metav1.ObjectMeta{
				Name: "nginx-config",
			},
			expected: true,
		},
		{
			name: "regex pattern with wildcard",
			change: karov1alpha1.ChangeSpec{
				Kind: "ConfigMap",
				Name: ".*nginx.*-config",
			},
			meta: metav1.ObjectMeta{
				Name: "frontend-nginx-app-config",
			},
			expected: true,
		},
		{
			name: "regex pattern with specific prefix",
			change: karov1alpha1.ChangeSpec{
				Kind: "Secret",
				Name: "^nginx.*-secret$",
			},
			meta: metav1.ObjectMeta{
				Name: "nginx-prod-secret",
			},
			expected: true,
		},
		{
			name: "regex pattern no match",
			change: karov1alpha1.ChangeSpec{
				Kind: "ConfigMap",
				Name: ".*apache.*-config",
			},
			meta: metav1.ObjectMeta{
				Name: "nginx-app-config",
			},
			expected: false,
		},
		{
			name: "regex pattern with character classes",
			change: karov1alpha1.ChangeSpec{
				Kind: "Secret",
				Name: "app-[0-9]+-secret",
			},
			meta: metav1.ObjectMeta{
				Name: "app-123-secret",
			},
			expected: true,
		},
		{
			name: "regex pattern with character classes no match",
			change: karov1alpha1.ChangeSpec{
				Kind: "Secret",
				Name: "app-[0-9]+-secret",
			},
			meta: metav1.ObjectMeta{
				Name: "app-abc-secret",
			},
			expected: false,
		},
		{
			name: "invalid regex falls back to exact match - should match",
			change: karov1alpha1.ChangeSpec{
				Kind: "ConfigMap",
				Name: "[invalid-regex",
			},
			meta: metav1.ObjectMeta{
				Name: "[invalid-regex",
			},
			expected: true,
		},
		{
			name: "invalid regex falls back to exact match - should not match",
			change: karov1alpha1.ChangeSpec{
				Kind: "ConfigMap",
				Name: "[invalid-regex",
			},
			meta: metav1.ObjectMeta{
				Name: "different-name",
			},
			expected: false,
		},
		{
			name: "empty name should not match",
			change: karov1alpha1.ChangeSpec{
				Kind: "ConfigMap",
				Name: "",
			},
			meta: metav1.ObjectMeta{
				Name: "any-name",
			},
			expected: false,
		},
		{
			name: "regex with OR operator",
			change: karov1alpha1.ChangeSpec{
				Kind: "ConfigMap",
				Name: "(nginx|apache)-config",
			},
			meta: metav1.ObjectMeta{
				Name: "apache-config",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := store.matchesNameOrSelector(tt.change, tt.meta)
			if result != tt.expected {
				t.Errorf("matchesNameOrSelector() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestMemoryRestartRuleStore_GetForSecret_Regex(t *testing.T) {
	tests := []struct {
		name      string
		secret    v1.Secret
		operation OperationType
		rules     []*karov1alpha1.RestartRule
		expected  int
	}{
		{
			name: "regex pattern matches multiple secrets",
			secret: v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "nginx-prod-secret",
					Namespace: "default",
				},
			},
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "Secret",
								Name: ".*nginx.*-secret",
							},
						},
					},
				},
			},
			expected: 1,
		},
		{
			name: "regex pattern does not match",
			secret: v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "apache-prod-secret",
					Namespace: "default",
				},
			},
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "Secret",
								Name: "^nginx.*-secret$",
							},
						},
					},
				},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := NewMemoryRestartRuleStore()
			ctx := context.Background()

			for _, rule := range tt.rules {
				store.Add(ctx, rule)
			}

			result := store.GetForSecret(ctx, tt.secret, tt.operation)
			if len(result) != tt.expected {
				t.Errorf("GetForSecret() returned %d rules, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestMemoryRestartRuleStore_GetForConfigMap_Regex(t *testing.T) {
	tests := []struct {
		name      string
		configMap v1.ConfigMap
		operation OperationType
		rules     []*karov1alpha1.RestartRule
		expected  int
	}{
		{
			name: "regex pattern matches configmap",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "frontend-nginx-app-config",
					Namespace: "default",
				},
			},
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "ConfigMap",
								Name: ".*nginx.*-config",
							},
						},
					},
				},
			},
			expected: 1,
		},
		{
			name: "multiple regex patterns match same configmap",
			configMap: v1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app-123-config",
					Namespace: "default",
				},
			},
			operation: OperationUpdate,
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule-1",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "ConfigMap",
								Name: "app-[0-9]+-config",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-rule-2",
						Namespace: "default",
					},
					Spec: karov1alpha1.RestartRuleSpec{
						Changes: []karov1alpha1.ChangeSpec{
							{
								Kind: "ConfigMap",
								Name: "app-.*-config",
							},
						},
					},
				},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			store := NewMemoryRestartRuleStore()
			ctx := context.Background()

			for _, rule := range tt.rules {
				store.Add(ctx, rule)
			}

			result := store.GetForConfigMap(ctx, tt.configMap, tt.operation)
			if len(result) != tt.expected {
				t.Errorf("GetForConfigMap() returned %d rules, want %d", len(result), tt.expected)
			}
		})
	}
}

func TestMemoryRestartRuleStore_matchesOperationType(t *testing.T) {
	store := NewMemoryRestartRuleStore()

	tests := []struct {
		name      string
		change    karov1alpha1.ChangeSpec
		operation OperationType
		expected  bool
	}{
		{
			name: "empty ChangeType only matches Update operations",
			change: karov1alpha1.ChangeSpec{
				Kind: "ConfigMap",
				Name: "test",
			},
			operation: OperationUpdate,
			expected:  true,
		},
		{
			name: "empty ChangeType does not match Create operations",
			change: karov1alpha1.ChangeSpec{
				Kind: "ConfigMap",
				Name: "test",
			},
			operation: OperationCreate,
			expected:  false,
		},
		{
			name: "empty ChangeType does not match Delete operations",
			change: karov1alpha1.ChangeSpec{
				Kind: "ConfigMap",
				Name: "test",
			},
			operation: OperationDelete,
			expected:  false,
		},
		{
			name: "matching operation type",
			change: karov1alpha1.ChangeSpec{
				Kind:       "ConfigMap",
				Name:       "test",
				ChangeType: []string{"Update"},
			},
			operation: OperationUpdate,
			expected:  true,
		},
		{
			name: "non-matching operation type",
			change: karov1alpha1.ChangeSpec{
				Kind:       "ConfigMap",
				Name:       "test",
				ChangeType: []string{"Create"},
			},
			operation: OperationUpdate,
			expected:  false,
		},
		{
			name: "multiple operation types with match",
			change: karov1alpha1.ChangeSpec{
				Kind:       "ConfigMap",
				Name:       "test",
				ChangeType: []string{"Create", "Update", "Delete"},
			},
			operation: OperationUpdate,
			expected:  true,
		},
		{
			name: "multiple operation types without match",
			change: karov1alpha1.ChangeSpec{
				Kind:       "ConfigMap",
				Name:       "test",
				ChangeType: []string{"Create", "Delete"},
			},
			operation: OperationUpdate,
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := store.matchesOperationType(tt.change, tt.operation)
			if result != tt.expected {
				t.Errorf("matchesOperationType() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
