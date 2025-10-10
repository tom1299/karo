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

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

func TestUpdateStatus(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	err := clientgoscheme.AddToScheme(scheme)
	g.Expect(err).NotTo(HaveOccurred())
	err = karov1alpha1.AddToScheme(scheme)
	g.Expect(err).NotTo(HaveOccurred())

	rule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-rule",
			Namespace: "default",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(rule).
		WithStatusSubresource(rule).
		Build()

	reconciler := &RestartRuleReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	ctx := context.Background()

	// Test valid transition
	err = reconciler.updateStatus(ctx, rule, "Pending", "TestReason", "Test message")
	g.Expect(err).NotTo(HaveOccurred())

	var updatedRule karov1alpha1.RestartRule
	err = fakeClient.Get(ctx, types.NamespacedName{Name: "test-rule", Namespace: "default"}, &updatedRule)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(updatedRule.Status.Phase).To(Equal("Pending"))

	// Test invalid transition
	err = reconciler.updateStatus(ctx, &updatedRule, "Failed", "TestReason", "Test message")
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("invalid phase transition"))
}

// TODO: Add more tests for updateStatus covering all valid and invalid transitions
func TestIsValidTransition(t *testing.T) {
	g := NewWithT(t)

	testCases := []struct {
		name     string
		from     string
		to       string
		expected bool
	}{
		{
			name:     "Valid: None to Pending",
			from:     "",
			to:       "Pending",
			expected: true,
		},
		{
			name:     "Valid: Pending to Active",
			from:     "Pending",
			to:       "Active",
			expected: true,
		},
		{
			name:     "Valid: Pending to Invalid",
			from:     "Pending",
			to:       "Invalid",
			expected: true,
		},
		{
			name:     "Valid: Active to Failed",
			from:     "Active",
			to:       "Failed",
			expected: true,
		},
		{
			name:     "Valid: Active to Active",
			from:     "Active",
			to:       "Active",
			expected: true,
		},
		{
			name:     "Invalid: None to Active",
			from:     "",
			to:       "Active",
			expected: false,
		},
		{
			name:     "Invalid: Pending to Failed",
			from:     "Pending",
			to:       "Failed",
			expected: false,
		},
		{
			name:     "Valid: Pending to Pending",
			from:     "Pending",
			to:       "Pending",
			expected: true,
		},
		{
			name:     "Invalid: Active to Pending",
			from:     "Active",
			to:       "Pending",
			expected: false,
		},
		{
			name:     "Invalid: Invalid to Active",
			from:     "Invalid",
			to:       "Active",
			expected: false,
		},
		{
			name:     "Valid: Failed to Failed",
			from:     "Failed",
			to:       "Failed",
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g.Expect(isValidTransition(tc.from, tc.to)).To(Equal(tc.expected))
		})
	}
}
