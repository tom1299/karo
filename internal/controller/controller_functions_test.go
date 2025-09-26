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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"karo.jeeatwork.com/internal/store"
)

const (
	statusFailed = "Failed"
)

var errUpdateFailed = errors.New("update failed")

// MockStatusWriter is a mock implementation of the client.StatusWriter interface for testing
type MockStatusWriter struct {
	mock.Mock
	client.StatusWriter
}

func (m *MockStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	args := m.Called(ctx, obj, subResource, opts)

	return args.Error(0)
}

func (m *MockStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	args := m.Called(ctx, obj)

	return args.Error(0)
}

func (m *MockStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	args := m.Called(ctx, obj, patch)

	return args.Error(0)
}

// MockClient is a mock implementation of the client.Client interface for testing
type MockClient struct {
	mock.Mock

	client.Client

	statusWriter client.StatusWriter
}

func (m *MockClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	args := m.Called(ctx, key, obj)
	if dep, ok := obj.(*appsv1.Deployment); ok {
		*dep = appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Annotations: make(map[string]string),
					},
				},
			},
		}
	} else if rule, ok := obj.(*karov1alpha1.RestartRule); ok {
		// Populate the rule with some data to avoid nil pointers
		*rule = karov1alpha1.RestartRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      key.Name,
				Namespace: key.Namespace,
			},
		}
	}

	return args.Error(0)
}

func (m *MockClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	args := m.Called(ctx, obj)

	return args.Error(0)
}

//nolint:ireturn
func (m *MockClient) Status() client.StatusWriter {
	if m.statusWriter == nil {
		m.statusWriter = &MockStatusWriter{}
	}

	return m.statusWriter
}

func TestProcessRestartRules_UniqueTargets(t *testing.T) {
	mockClient := new(MockClient)
	mockStatusWriter, ok := mockClient.Status().(*MockStatusWriter)
	assert.True(t, ok)

	reconciler := &BaseReconciler{
		Client:           mockClient,
		RestartRuleStore: store.NewMemoryRestartRuleStore(),
	}

	// Define two rules targeting the same deployment
	rule1 := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
		Spec: karov1alpha1.RestartRuleSpec{
			Targets: []karov1alpha1.TargetSpec{
				{Kind: "Deployment", Name: "test-deployment"},
			},
		},
	}
	rule2 := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{Name: "rule2", Namespace: "default"},
		Spec: karov1alpha1.RestartRuleSpec{
			Targets: []karov1alpha1.TargetSpec{
				{Kind: "Deployment", Name: "test-deployment"},
			},
		},
	}
	restartRules := []*karov1alpha1.RestartRule{rule1, rule2}

	// Mock Get and Update calls for the deployment
	mockClient.On("Get", mock.Anything, mock.AnythingOfType("types.NamespacedName"), mock.AnythingOfType("*v1.Deployment")).Return(nil)
	mockClient.On("Update", mock.Anything, mock.AnythingOfType("*v1.Deployment")).Return(nil)

	// Mock Get and Status().Update for the RestartRule
	mockClient.On("Get", mock.Anything, mock.AnythingOfType("types.NamespacedName"), mock.AnythingOfType("*v1alpha1.RestartRule")).Return(nil)
	mockStatusWriter.On("Update", mock.Anything, mock.AnythingOfType("*v1alpha1.RestartRule")).Return(nil)

	// Process the rules
	err := reconciler.ProcessRestartRules(context.Background(), restartRules, "test-configmap", "ConfigMap")
	assert.NoError(t, err)

	// Verify calls
	mockClient.AssertNumberOfCalls(t, "Update", 1)
	mockStatusWriter.AssertNumberOfCalls(t, "Update", 2)
}

func TestProcessRestartRules_RestartFailure(t *testing.T) {
	mockClient := new(MockClient)
	mockStatusWriter, ok := mockClient.Status().(*MockStatusWriter)
	assert.True(t, ok)

	reconciler := &BaseReconciler{
		Client:           mockClient,
		RestartRuleStore: store.NewMemoryRestartRuleStore(),
	}

	rule1 := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
		Spec: karov1alpha1.RestartRuleSpec{
			Targets: []karov1alpha1.TargetSpec{
				{Kind: "Deployment", Name: "test-deployment"},
			},
		},
	}
	restartRules := []*karov1alpha1.RestartRule{rule1}

	// Mock a failed deployment update
	mockClient.On("Get", mock.Anything, mock.AnythingOfType("types.NamespacedName"), mock.AnythingOfType("*v1.Deployment")).Return(nil)
	mockClient.On("Update", mock.Anything, mock.AnythingOfType("*v1.Deployment")).Return(errUpdateFailed)

	// Mock Get and Status().Update for the RestartRule
	mockClient.On("Get", mock.Anything, mock.AnythingOfType("types.NamespacedName"), mock.AnythingOfType("*v1alpha1.RestartRule")).Return(nil)
	mockStatusWriter.On("Update", mock.Anything, mock.MatchedBy(func(rule *karov1alpha1.RestartRule) bool {
		return len(rule.Status.RestartHistory) > 0 && rule.Status.RestartHistory[0].Status == statusFailed
	})).Return(nil)

	err := reconciler.ProcessRestartRules(context.Background(), restartRules, "test-configmap", "ConfigMap")
	assert.NoError(t, err)

	// Verify that the status of the rule reflects the failure
	mockStatusWriter.AssertCalled(t, "Update", mock.Anything, mock.MatchedBy(func(rule *karov1alpha1.RestartRule) bool {
		return len(rule.Status.RestartHistory) > 0 && rule.Status.RestartHistory[0].Status == statusFailed
	}))
	mockStatusWriter.AssertNumberOfCalls(t, "Update", 1)
}
