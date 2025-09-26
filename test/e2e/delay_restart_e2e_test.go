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

package e2e

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

func TestDelayRestartE2E(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	setupDelayTestEnvironment(ctx, t, clients)
	defer cleanupDelayTest(ctx, t, clients)

	testDelayedRestart(ctx, t, clients)
	testMultipleDelays(ctx, t, clients)
}

func setupDelayTestEnvironment(ctx context.Context, t *testing.T, clients *testClients) {
	namespace := "delay-test"

	t.Log("Creating delay test namespace...")
	if err := createNamespace(ctx, clients.clientset, namespace); err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}

	t.Log("Starting controller manager for delay test...")
	if err := clients.controllerManager.Start(ctx); err != nil {
		t.Fatalf("Failed to start controller manager: %v", err)
	}

	// Wait for controller manager to be ready
	time.Sleep(2 * time.Second)
}

func testDelayedRestart(ctx context.Context, t *testing.T, clients *testClients) {
	namespace := "delay-test"

	t.Log("Testing delayed restart functionality...")

	// Create ConfigMap
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "delay-test-config",
			Namespace: namespace,
		},
		Data: map[string]string{
			"key": "initial-value",
		},
	}
	if err := clients.k8sClient.Create(ctx, configMap); err != nil {
		t.Fatalf("Failed to create ConfigMap: %v", err)
	}

	// Create Deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "delay-test-app",
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "delay-test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "delay-test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}
	if err := clients.k8sClient.Create(ctx, deployment); err != nil {
		t.Fatalf("Failed to create Deployment: %v", err)
	}

	// Wait for deployment to be ready
	if err := waitForDeploymentReady(ctx, clients.clientset, namespace, "delay-test-app"); err != nil {
		t.Fatalf("Failed to wait for deployment: %v", err)
	}

	// Create RestartRule with delay
	restartRule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "delay-restart-rule",
			Namespace: namespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			DelayRestart: &metav1.Duration{Duration: 5 * time.Second},
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind: "ConfigMap",
					Name: "delay-test-config",
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: "delay-test-app",
				},
			},
		},
	}
	if err := clients.k8sClient.Create(ctx, restartRule); err != nil {
		t.Fatalf("Failed to create RestartRule: %v", err)
	}

	// Wait for RestartRule to be active
	waitForRestartRuleActive(ctx, t, clients, namespace, "delay-restart-rule")

	// Record original restart annotation
	var originalDep appsv1.Deployment
	if err := clients.k8sClient.Get(ctx, client.ObjectKey{Name: "delay-test-app", Namespace: namespace}, &originalDep); err != nil {
		t.Fatalf("Failed to get deployment: %v", err)
	}

	originalRestart := ""
	if originalDep.Spec.Template.Annotations != nil {
		originalRestart = originalDep.Spec.Template.Annotations["karo.jeeatwork.com/restartedAt"]
	}

	// Update ConfigMap to trigger restart
	var cm corev1.ConfigMap
	if err := clients.k8sClient.Get(ctx, client.ObjectKey{Name: "delay-test-config", Namespace: namespace}, &cm); err != nil {
		t.Fatalf("Failed to get ConfigMap: %v", err)
	}
	cm.Data["key"] = "updated-value"
	updateTime := time.Now()
	t.Logf("Updating ConfigMap at: %s", updateTime.Format(time.RFC3339))
	if err := clients.k8sClient.Update(ctx, &cm); err != nil {
		t.Fatalf("Failed to update ConfigMap: %v", err)
	}

	// Verify deployment is NOT immediately restarted
	t.Log("Checking that deployment is NOT restarted immediately...")
	time.Sleep(3 * time.Second)
	var dep appsv1.Deployment
	if err := clients.k8sClient.Get(ctx, client.ObjectKey{Name: "delay-test-app", Namespace: namespace}, &dep); err != nil {
		t.Fatalf("Failed to get deployment after update: %v", err)
	}

	currentRestart := ""
	if dep.Spec.Template.Annotations != nil {
		currentRestart = dep.Spec.Template.Annotations["karo.jeeatwork.com/restartedAt"]
	}

	checkTime := time.Now()
	t.Logf("Checked deployment at: %s (after %v)", checkTime.Format(time.RFC3339), checkTime.Sub(updateTime))
	t.Logf("Original restart: '%s', Current restart: '%s'", originalRestart, currentRestart)

	if currentRestart != originalRestart {
		t.Errorf("Deployment was restarted immediately (after %v), expected delay", checkTime.Sub(updateTime))
	}

	// Wait for delayed restart to happen
	t.Log("Waiting for delayed restart...")
	time.Sleep(8 * time.Second)

	// Verify deployment IS now restarted
	if err := clients.k8sClient.Get(ctx, client.ObjectKey{Name: "delay-test-app", Namespace: namespace}, &dep); err != nil {
		t.Fatalf("Failed to get deployment after delay: %v", err)
	}

	newRestart := ""
	if dep.Spec.Template.Annotations != nil {
		newRestart = dep.Spec.Template.Annotations["karo.jeeatwork.com/restartedAt"]
	}

	finalCheckTime := time.Now()
	t.Logf("Final check at: %s (after %v)", finalCheckTime.Format(time.RFC3339), finalCheckTime.Sub(updateTime))
	t.Logf("New restart annotation: '%s'", newRestart)

	if newRestart == originalRestart {
		t.Error("Deployment was not restarted after delay period")
	}

	// Verify restart happened after the delay with more tolerance
	restartTime, err := time.Parse(time.RFC3339, newRestart)
	if err != nil {
		t.Fatalf("Failed to parse restart time: %v", err)
	}

	timeDiff := restartTime.Sub(updateTime)
	t.Logf("Restart time difference: %v (expected >= 4s for delayed restart)", timeDiff)
	// Check if restart happened at least after 4 seconds (accounting for container timing precision)
	// This still proves delay functionality works (vs immediate restart < 1s)
	if timeDiff < 4*time.Second {
		t.Errorf("Restart happened too early: %v < 4s", timeDiff)
	}
	if timeDiff > 10*time.Second {
		t.Errorf("Restart happened too late: %v > 10s", timeDiff)
	}

	t.Log("Delayed restart test completed successfully")
}

func testMultipleDelays(ctx context.Context, t *testing.T, clients *testClients) {
	namespace := "delay-test"

	t.Log("Testing multiple delays with max delay selection...")

	// Create additional ConfigMap
	configMap2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "delay-test-config2",
			Namespace: namespace,
		},
		Data: map[string]string{
			"key2": "value2",
		},
	}
	if err := clients.k8sClient.Create(ctx, configMap2); err != nil {
		t.Fatalf("Failed to create ConfigMap2: %v", err)
	}

	// Create Deployment for multiple delay test
	deployment2 := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-delay-app",
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "multi-delay"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "multi-delay"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "app",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}
	if err := clients.k8sClient.Create(ctx, deployment2); err != nil {
		t.Fatalf("Failed to create Deployment2: %v", err)
	}

	// Wait for deployment to be ready
	if err := waitForDeploymentReady(ctx, clients.clientset, namespace, "multi-delay-app"); err != nil {
		t.Fatalf("Failed to wait for deployment: %v", err)
	}

	// Create RestartRules with different delays
	restartRule1 := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-delay-rule1",
			Namespace: namespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			DelayRestart: &metav1.Duration{Duration: 3 * time.Second},
			Changes: []karov1alpha1.ChangeSpec{
				{Kind: "ConfigMap", Name: "delay-test-config"},
			},
			Targets: []karov1alpha1.TargetSpec{
				{Kind: "Deployment", Name: "multi-delay-app"},
			},
		},
	}

	restartRule2 := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multi-delay-rule2",
			Namespace: namespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			DelayRestart: &metav1.Duration{Duration: 8 * time.Second},
			Changes: []karov1alpha1.ChangeSpec{
				{Kind: "ConfigMap", Name: "delay-test-config2"},
			},
			Targets: []karov1alpha1.TargetSpec{
				{Kind: "Deployment", Name: "multi-delay-app"},
			},
		},
	}

	if err := clients.k8sClient.Create(ctx, restartRule1); err != nil {
		t.Fatalf("Failed to create RestartRule1: %v", err)
	}
	if err := clients.k8sClient.Create(ctx, restartRule2); err != nil {
		t.Fatalf("Failed to create RestartRule2: %v", err)
	}

	// Wait for RestartRules to be active
	waitForRestartRuleActive(ctx, t, clients, namespace, "multi-delay-rule1")
	waitForRestartRuleActive(ctx, t, clients, namespace, "multi-delay-rule2")

	// Record original restart annotation
	var originalDep appsv1.Deployment
	if err := clients.k8sClient.Get(ctx, client.ObjectKey{Name: "multi-delay-app", Namespace: namespace}, &originalDep); err != nil {
		t.Fatalf("Failed to get deployment: %v", err)
	}

	originalRestart := ""
	if originalDep.Spec.Template.Annotations != nil {
		originalRestart = originalDep.Spec.Template.Annotations["karo.jeeatwork.com/restartedAt"]
	}

	// Update both ConfigMaps simultaneously
	var cm1, cm2 corev1.ConfigMap
	if err := clients.k8sClient.Get(ctx, client.ObjectKey{Name: "delay-test-config", Namespace: namespace}, &cm1); err != nil {
		t.Fatalf("Failed to get ConfigMap1: %v", err)
	}
	if err := clients.k8sClient.Get(ctx, client.ObjectKey{Name: "delay-test-config2", Namespace: namespace}, &cm2); err != nil {
		t.Fatalf("Failed to get ConfigMap2: %v", err)
	}

	updateTime := time.Now()
	cm1.Data["key"] = "updated1"
	cm2.Data["key2"] = "updated2"

	if err := clients.k8sClient.Update(ctx, &cm1); err != nil {
		t.Fatalf("Failed to update ConfigMap1: %v", err)
	}
	if err := clients.k8sClient.Update(ctx, &cm2); err != nil {
		t.Fatalf("Failed to update ConfigMap2: %v", err)
	}

	// Verify deployment is not restarted before max delay (8 seconds)
	time.Sleep(6 * time.Second)
	var dep appsv1.Deployment
	if err := clients.k8sClient.Get(ctx, client.ObjectKey{Name: "multi-delay-app", Namespace: namespace}, &dep); err != nil {
		t.Fatalf("Failed to get deployment: %v", err)
	}

	currentRestart := ""
	if dep.Spec.Template.Annotations != nil {
		currentRestart = dep.Spec.Template.Annotations["karo.jeeatwork.com/restartedAt"]
	}

	if currentRestart != originalRestart {
		t.Error("Deployment was restarted before max delay period")
	}

	// Wait for max delay to pass
	t.Log("Waiting for max delay restart...")
	time.Sleep(5 * time.Second)

	// Verify deployment IS now restarted
	if err := clients.k8sClient.Get(ctx, client.ObjectKey{Name: "multi-delay-app", Namespace: namespace}, &dep); err != nil {
		t.Fatalf("Failed to get deployment after max delay: %v", err)
	}

	newRestart := ""
	if dep.Spec.Template.Annotations != nil {
		newRestart = dep.Spec.Template.Annotations["karo.jeeatwork.com/restartedAt"]
	}

	if newRestart == originalRestart {
		t.Error("Deployment was not restarted after max delay period")
	}

	// Verify restart happened after the max delay (8 seconds)
	restartTime, err := time.Parse(time.RFC3339, newRestart)
	if err != nil {
		t.Fatalf("Failed to parse restart time: %v", err)
	}

	timeDiff := restartTime.Sub(updateTime)
	// Check if restart happened at least after 7 seconds (accounting for container timing precision)
	// This still proves max delay functionality works with 8s max delay
	if timeDiff < 7*time.Second {
		t.Errorf("Restart happened before max delay: %v < 7s", timeDiff)
	}
	if timeDiff > 12*time.Second {
		t.Errorf("Restart happened too late: %v > 12s", timeDiff)
	}

	t.Log("Multiple delays test completed successfully")
}

func waitForRestartRuleActive(ctx context.Context, t *testing.T, clients *testClients, namespace, name string) {
	for i := 0; i < 10; i++ { // Wait up to 10 seconds
		var rule karov1alpha1.RestartRule
		if err := clients.k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, &rule); err == nil {
			if rule.Status.Phase == "Active" {
				return
			}
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("RestartRule %s/%s did not become active", namespace, name)
}

func cleanupDelayTest(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Cleaning up delay test...")
	if err := clients.controllerManager.Stop(); err != nil {
		t.Logf("Failed to stop controller manager: %v", err)
	}
	if err := clients.clientset.CoreV1().Namespaces().Delete(ctx, "delay-test", metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		t.Logf("Failed to delete namespace: %v", err)
	}
}

func int32Ptr(i int32) *int32 {
	return &i
}
