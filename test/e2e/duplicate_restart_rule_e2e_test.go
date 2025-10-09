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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	duplicateTestNamespace     = "karo-duplicate-e2e-test"
	testDeploymentName         = "test-deployment"
	testConfigMapName          = "test-config-map"
	restartRule1Name           = "test-restart-rule-1"
	restartRule2Name           = "test-restart-rule-2"
	duplicateRestartAnnotation = "karo.jeeatwork.com/restartedAt"
)

// TestDuplicateRestartRuleE2E tests that when two restart rules target the same deployment,
// the deployment is only restarted once when the monitored resource changes.
//
// Given: A deployment named test-deployment
// And: A configmap named test-config-map
// And: Two restart rules for the deployment test-deployment as a target and the configmap test-config-map
// When: The configmap test-config-map is changed
// Then: The deployment should be restarted only once
func TestDuplicateRestartRuleE2E(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	setupDuplicateTestEnvironment(ctx, t, clients)
	defer cleanupDuplicateTest(ctx, t, clients)

	testDeploymentRestartedOnlyOnce(ctx, t, clients)

	t.Log("=== Duplicate restart rule test completed successfully ===")
}

func setupDuplicateTestEnvironment(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Creating test namespace for duplicate restart rule test...")
	if err := createNamespace(ctx, clients.clientset, duplicateTestNamespace); err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}

	t.Log("Starting controller manager...")
	if err := clients.controllerManager.Start(ctx); err != nil {
		t.Fatalf("Failed to start controller manager: %v", err)
	}

	t.Log("Creating test ConfigMap...")
	configMap := createTestConfigMap()
	if err := clients.k8sClient.Create(ctx, configMap); err != nil {
		t.Fatalf("Failed to create ConfigMap: %v", err)
	}

	t.Log("Creating test Deployment...")
	deployment := createTestDeployment()
	if err := clients.k8sClient.Create(ctx, deployment); err != nil {
		t.Fatalf("Failed to create Deployment: %v", err)
	}

	t.Log("Waiting for test Deployment to be ready...")
	if err := waitForDeploymentReady(ctx, clients.clientset, duplicateTestNamespace, testDeploymentName); err != nil {
		t.Fatalf("Deployment did not become ready: %v", err)
	}
}

func createTestConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testConfigMapName,
			Namespace: duplicateTestNamespace,
			Labels: map[string]string{
				"app": "test",
			},
		},
		Data: map[string]string{
			"config.yaml": `
server:
  port: 8080
  host: localhost
`,
		},
	}
}

func createTestDeployment() *appsv1.Deployment {
	replicas := int32(1)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testDeploymentName,
			Namespace: duplicateTestNamespace,
			Labels: map[string]string{
				"app": "test",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "test",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "test",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-app",
							Image: "nginx:1.21-alpine",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 8080,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "config",
									MountPath: "/etc/config",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: testConfigMapName,
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func createTestRestartRule1() *karov1alpha1.RestartRule {
	return &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restartRule1Name,
			Namespace: duplicateTestNamespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "ConfigMap",
					Name:       testConfigMapName,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: testDeploymentName,
				},
			},
		},
	}
}

func createTestRestartRule2() *karov1alpha1.RestartRule {
	return &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restartRule2Name,
			Namespace: duplicateTestNamespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "ConfigMap",
					Name:       testConfigMapName,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: testDeploymentName,
				},
			},
		},
	}
}

func testDeploymentRestartedOnlyOnce(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("=== Testing that deployment is restarted only once with duplicate restart rules ===")

	// Create first RestartRule
	t.Log("Creating first RestartRule...")
	restartRule1 := createTestRestartRule1()
	if err := clients.k8sClient.Create(ctx, restartRule1); err != nil {
		t.Fatalf("Failed to create first RestartRule: %v", err)
	}

	t.Log("Waiting for first RestartRule to become Active...")
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, duplicateTestNamespace, restartRule1Name); err != nil {
		t.Fatalf("First RestartRule did not become ready: %v", err)
	}

	// Create second RestartRule (duplicate)
	t.Log("Creating second RestartRule (duplicate)...")
	restartRule2 := createTestRestartRule2()
	if err := clients.k8sClient.Create(ctx, restartRule2); err != nil {
		t.Fatalf("Failed to create second RestartRule: %v", err)
	}

	t.Log("Waiting for second RestartRule to become Active...")
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, duplicateTestNamespace, restartRule2Name); err != nil {
		t.Fatalf("Second RestartRule did not become ready: %v", err)
	}

	// Get initial restart annotation value (should be empty initially)
	initialRestartTime := getDeploymentRestartAnnotation(ctx, t, clients.k8sClient)
	t.Logf("Initial restart annotation: %s", initialRestartTime)

	// Update ConfigMap to trigger restart
	t.Log("Updating ConfigMap to trigger restart...")
	if err := updateTestConfigMapContent(ctx, clients.k8sClient); err != nil {
		t.Fatalf("Failed to update ConfigMap: %v", err)
	}

	// Wait for deployment to be restarted (annotation should be added)
	t.Log("Waiting for deployment restart annotation to appear...")
	firstRestartTime := waitForDeploymentRestartAnnotation(ctx, t, clients.k8sClient, initialRestartTime)
	t.Logf("First restart annotation detected: %s", firstRestartTime)

	// Wait a bit to ensure controller has time to process both restart rules
	// If the deployment is restarted twice, we should see the annotation change again
	t.Log("Waiting to ensure no duplicate restart occurs...")
	time.Sleep(10 * time.Second)

	// Check that the restart annotation has NOT changed (meaning only one restart occurred)
	currentRestartTime := getDeploymentRestartAnnotation(ctx, t, clients.k8sClient)
	t.Logf("Current restart annotation after wait: %s", currentRestartTime)

	if currentRestartTime != firstRestartTime {
		t.Fatalf("Deployment was restarted multiple times! First restart: %s, Current: %s",
			firstRestartTime, currentRestartTime)
	}

	// Verify that both restart rules recorded the restart event
	t.Log("Verifying that both RestartRules recorded the restart event...")
	if err := waitForRestartEvent(ctx, clients.k8sClient, duplicateTestNamespace,
		restartRule1Name, testDeploymentName); err != nil {
		t.Fatalf("First RestartRule did not record restart event: %v", err)
	}

	if err := waitForRestartEvent(ctx, clients.k8sClient, duplicateTestNamespace,
		restartRule2Name, testDeploymentName); err != nil {
		t.Fatalf("Second RestartRule did not record restart event: %v", err)
	}

	t.Log("SUCCESS: Deployment was restarted only once despite two restart rules, and both rules recorded the event")
}

func updateTestConfigMapContent(ctx context.Context, k8sClient client.Client) error {
	updatedConfigMap := &corev1.ConfigMap{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: duplicateTestNamespace, Name: testConfigMapName}, updatedConfigMap); err != nil {
		return err
	}

	updatedConfigMap.Data["config.yaml"] = `
server:
  port: 8080
  host: 0.0.0.0
  debug: true
`

	return k8sClient.Update(ctx, updatedConfigMap)
}

func getDeploymentRestartAnnotation(ctx context.Context, t *testing.T, k8sClient client.Client) string {
	deployment := &appsv1.Deployment{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: duplicateTestNamespace, Name: testDeploymentName}, deployment); err != nil {
		t.Fatalf("Failed to get deployment: %v", err)
	}

	if annotations := deployment.Spec.Template.Annotations; annotations != nil {
		if restartTime, exists := annotations[duplicateRestartAnnotation]; exists {
			return restartTime
		}
	}

	return ""
}

func waitForDeploymentRestartAnnotation(
	ctx context.Context,
	t *testing.T,
	k8sClient client.Client,
	previousRestartTime string,
) string {
	var restartTime string

	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		currentDeployment := &appsv1.Deployment{}
		if err := k8sClient.Get(
			ctx, client.ObjectKey{Namespace: duplicateTestNamespace, Name: testDeploymentName}, currentDeployment); err != nil {
			return false, err
		}

		if annotations := currentDeployment.Spec.Template.Annotations; annotations != nil {
			if currentRestartTime, exists := annotations[duplicateRestartAnnotation]; exists {
				if previousRestartTime == "" || currentRestartTime != previousRestartTime {
					t.Logf("Found restart annotation: %s = %s", duplicateRestartAnnotation, currentRestartTime)
					restartTime = currentRestartTime

					return true, nil
				}
				t.Logf("Restart annotation exists but hasn't changed: %s", currentRestartTime)
			}
		}

		t.Logf("Restart annotation not found, current annotations: %v",
			currentDeployment.Spec.Template.Annotations)

		return false, nil
	})

	if err != nil {
		t.Fatalf("Error while waiting for deployment restart: %v", err)
	}

	if restartTime == "" {
		t.Fatalf("Deployment was not restarted")
	}

	return restartTime
}

func cleanupDuplicateTest(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Cleaning up duplicate test resources...")

	// Stop controller manager first
	if err := clients.controllerManager.Stop(); err != nil {
		t.Logf("Error stopping controller manager: %v", err)
	}

	cleanupDuplicateRestartRules(ctx, t, clients.k8sClient)
	cleanupDuplicateK8sResources(ctx, t, clients.k8sClient)
	cleanupNamespaceByName(ctx, t, clients.clientset, duplicateTestNamespace)
}

func cleanupDuplicateRestartRules(ctx context.Context, t *testing.T, k8sClient client.Client) {
	restartRules := []*karov1alpha1.RestartRule{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      restartRule1Name,
				Namespace: duplicateTestNamespace,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      restartRule2Name,
				Namespace: duplicateTestNamespace,
			},
		},
	}

	for _, rule := range restartRules {
		deleteResource(ctx, t, k8sClient, rule, "RestartRule")
	}
}

func cleanupDuplicateK8sResources(ctx context.Context, t *testing.T, k8sClient client.Client) {
	resources := []client.Object{
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testDeploymentName,
				Namespace: duplicateTestNamespace,
			},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testConfigMapName,
				Namespace: duplicateTestNamespace,
			},
		},
	}

	for _, resource := range resources {
		resourceType := getResourceType(resource)
		deleteResource(ctx, t, k8sClient, resource, resourceType)
	}

	time.Sleep(2 * time.Second)
}
