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
	"k8s.io/apimachinery/pkg/util/wait"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	manager "karo.jeeatwork.com/internal/manager"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	namespacePrefix   = "karo-delay-e2e-test"
	restartAnnotation = "karo.jeeatwork.com/restartedAt"
)

// Helper function to create int32 pointer
func int32Ptr(i int32) *int32 {
	return &i
}

//nolint:unparam // Test helper function - prefix parameter used for consistency
func generateTestNamespace(prefix string) string {
	timestamp := time.Now().Format("150405-000") // HHmmss-ms (10 chars)
	// Ensure total length stays under 63 characters
	maxPrefixLen := 63 - len(timestamp) - 1 // -1 for the hyphen
	if len(prefix) > maxPrefixLen {
		prefix = prefix[:maxPrefixLen]
	}

	return prefix + "-" + timestamp
}

//nolint:dupl // Similar test patterns expected for comprehensive test coverage
func TestDelayedRestartBasic(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	namespace := generateTestNamespace(namespacePrefix)
	setupDelayTestEnvironment(ctx, t, clients, namespace)
	defer cleanupDelayTest(ctx, t, clients, namespace)

	t.Log("=== Testing basic delayed restart (5 seconds) ===")

	// Create deployment and ConfigMap
	t.Log("Creating test deployment...")
	deployment := createDelayTestDeployment(namespace, "delay-basic-deployment")
	if err := clients.k8sClient.Create(ctx, deployment); err != nil {
		t.Fatalf("Failed to create Deployment: %v", err)
	}

	if err := waitForDeploymentReady(ctx, clients.clientset, namespace, deployment.Name); err != nil {
		t.Fatalf("Deployment did not become ready: %v", err)
	}

	t.Log("Creating test ConfigMap...")
	configMap := createDelayTestConfigMap(namespace, "delay-basic-config")
	if err := clients.k8sClient.Create(ctx, configMap); err != nil {
		t.Fatalf("Failed to create ConfigMap: %v", err)
	}

	// Create RestartRule with 5 second delay
	t.Log("Creating RestartRule with 5 second delay...")
	restartRule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "delay-basic-rule",
			Namespace: namespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			DelayRestart: int32Ptr(5),
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "ConfigMap",
					Name:       configMap.Name,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: deployment.Name,
				},
			},
		},
	}
	if err := clients.k8sClient.Create(ctx, restartRule); err != nil {
		t.Fatalf("Failed to create RestartRule: %v", err)
	}

	t.Log("Waiting for RestartRule to become Active...")
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, namespace, restartRule.Name); err != nil {
		t.Fatalf("RestartRule did not become ready: %v", err)
	}

	// Get initial restart annotation
	initialAnnotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)

	// Update ConfigMap to trigger restart
	t.Log("Updating ConfigMap to trigger delayed restart...")
	if err := updateDelayTestConfigMapContent(ctx, clients.k8sClient, namespace, configMap.Name); err != nil {
		t.Fatalf("Failed to update ConfigMap: %v", err)
	}

	// Verify restart hasn't happened immediately (within 2 seconds)
	t.Log("Verifying restart doesn't happen immediately...")
	time.Sleep(2 * time.Second)
	annotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)
	if annotation != initialAnnotation {
		t.Errorf("Restart happened too early! Expected delay of 5 seconds, but restart occurred within 2 seconds")
	}

	// Wait for delay to complete and verify restart happened
	t.Log("Waiting for delayed restart to occur...")
	err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 15*time.Second, true, func(ctx context.Context) (bool, error) {
		annotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)

		return annotation != initialAnnotation, nil
	})
	if err != nil {
		t.Fatalf("Restart did not happen after delay period: %v", err)
	}

	t.Log("SUCCESS: Deployment was restarted after 5 second delay")
}

//nolint:cyclop // E2E test complexity acceptable for comprehensive test coverage
func TestDelayedRestartMultipleRulesHighestWins(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	namespace := generateTestNamespace(namespacePrefix)
	setupDelayTestEnvironment(ctx, t, clients, namespace)
	defer cleanupDelayTest(ctx, t, clients, namespace)

	t.Log("=== Testing multiple rules with different delays (highest delay should win) ===")

	// Create deployment
	t.Log("Creating test deployment...")
	deployment := createDelayTestDeployment(namespace, "delay-multi-deployment")
	if err := clients.k8sClient.Create(ctx, deployment); err != nil {
		t.Fatalf("Failed to create Deployment: %v", err)
	}

	if err := waitForDeploymentReady(ctx, clients.clientset, namespace, deployment.Name); err != nil {
		t.Fatalf("Deployment did not become ready: %v", err)
	}

	// Create one ConfigMap that will be watched by both rules
	t.Log("Creating test ConfigMap...")
	configMap := createDelayTestConfigMap(namespace, "delay-multi-config")
	if err := clients.k8sClient.Create(ctx, configMap); err != nil {
		t.Fatalf("Failed to create ConfigMap: %v", err)
	}

	// Create RestartRule1 with 3 second delay
	t.Log("Creating RestartRule1 with 3 second delay...")
	restartRule1 := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "delay-multi-rule1",
			Namespace: namespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			DelayRestart: int32Ptr(3),
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "ConfigMap",
					Name:       configMap.Name,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: deployment.Name,
				},
			},
		},
	}
	if err := clients.k8sClient.Create(ctx, restartRule1); err != nil {
		t.Fatalf("Failed to create RestartRule1: %v", err)
	}

	// Create RestartRule2 with 7 second delay
	t.Log("Creating RestartRule2 with 7 second delay...")
	restartRule2 := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "delay-multi-rule2",
			Namespace: namespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			DelayRestart: int32Ptr(7),
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "ConfigMap",
					Name:       configMap.Name,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: deployment.Name,
				},
			},
		},
	}
	if err := clients.k8sClient.Create(ctx, restartRule2); err != nil {
		t.Fatalf("Failed to create RestartRule2: %v", err)
	}

	t.Log("Waiting for RestartRules to become Active...")
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, namespace, restartRule1.Name); err != nil {
		t.Fatalf("RestartRule1 did not become ready: %v", err)
	}
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, namespace, restartRule2.Name); err != nil {
		t.Fatalf("RestartRule2 did not become ready: %v", err)
	}

	// Get initial restart annotation
	initialAnnotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)

	// Update ConfigMap to trigger both rules
	t.Log("Updating ConfigMap to trigger restart (both rules watching)...")
	if err := updateDelayTestConfigMapContent(ctx, clients.k8sClient, namespace, configMap.Name); err != nil {
		t.Fatalf("Failed to update ConfigMap: %v", err)
	}

	// Verify restart hasn't happened within 4 seconds (should wait for the 7 second rule, not 3)
	t.Log("Verifying restart doesn't happen within 4 seconds (should use highest delay)...")
	time.Sleep(4 * time.Second)
	annotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)
	if annotation != initialAnnotation {
		t.Errorf("Restart happened too early! Expected delay of 7 seconds (highest), but restart occurred within 4 seconds")
	}

	// Wait for restart to occur (should happen after 7 seconds, not 3)
	t.Log("Waiting for delayed restart to occur...")
	err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 15*time.Second, true, func(ctx context.Context) (bool, error) {
		annotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)

		return annotation != initialAnnotation, nil
	})
	if err != nil {
		t.Fatalf("Restart did not happen after delay period: %v", err)
	}

	t.Log("SUCCESS: Deployment was restarted using the highest delay (7 seconds)")
}

//nolint:cyclop // E2E test complexity acceptable for comprehensive test coverage
func TestDelayedRestartMixedDelayAndNoDelay(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	namespace := generateTestNamespace(namespacePrefix)
	setupDelayTestEnvironment(ctx, t, clients, namespace)
	defer cleanupDelayTest(ctx, t, clients, namespace)

	t.Log("=== Testing mixed delay and no-delay rules (delay should take precedence) ===")

	// Create deployment
	t.Log("Creating test deployment...")
	deployment := createDelayTestDeployment(namespace, "delay-mixed-deployment")
	if err := clients.k8sClient.Create(ctx, deployment); err != nil {
		t.Fatalf("Failed to create Deployment: %v", err)
	}

	if err := waitForDeploymentReady(ctx, clients.clientset, namespace, deployment.Name); err != nil {
		t.Fatalf("Deployment did not become ready: %v", err)
	}

	// Create one ConfigMap that will be watched by both rules
	t.Log("Creating test ConfigMap...")
	configMap := createDelayTestConfigMap(namespace, "delay-mixed-config")
	if err := clients.k8sClient.Create(ctx, configMap); err != nil {
		t.Fatalf("Failed to create ConfigMap: %v", err)
	}

	// Create RestartRule1 with no delay
	t.Log("Creating RestartRule1 with no delay...")
	restartRule1 := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "delay-mixed-rule1",
			Namespace: namespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "ConfigMap",
					Name:       configMap.Name,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: deployment.Name,
				},
			},
		},
	}
	if err := clients.k8sClient.Create(ctx, restartRule1); err != nil {
		t.Fatalf("Failed to create RestartRule1: %v", err)
	}

	// Create RestartRule2 with 6 second delay
	t.Log("Creating RestartRule2 with 6 second delay...")
	restartRule2 := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "delay-mixed-rule2",
			Namespace: namespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			DelayRestart: int32Ptr(6),
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "ConfigMap",
					Name:       configMap.Name,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: deployment.Name,
				},
			},
		},
	}
	if err := clients.k8sClient.Create(ctx, restartRule2); err != nil {
		t.Fatalf("Failed to create RestartRule2: %v", err)
	}

	t.Log("Waiting for RestartRules to become Active...")
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, namespace, restartRule1.Name); err != nil {
		t.Fatalf("RestartRule1 did not become ready: %v", err)
	}
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, namespace, restartRule2.Name); err != nil {
		t.Fatalf("RestartRule2 did not become ready: %v", err)
	}

	// Get initial restart annotation
	initialAnnotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)

	// Update ConfigMap to trigger both rules
	t.Log("Updating ConfigMap to trigger restart (both rules watching)...")
	if err := updateDelayTestConfigMapContent(ctx, clients.k8sClient, namespace, configMap.Name); err != nil {
		t.Fatalf("Failed to update ConfigMap: %v", err)
	}

	// Verify restart is delayed despite no-delay rule
	t.Log("Verifying restart is delayed despite no-delay rule...")
	time.Sleep(3 * time.Second)
	annotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)
	if annotation != initialAnnotation {
		//nolint:lll // Error message provides necessary detail
		t.Errorf("Restart happened too early! Expected delay of 6 seconds due to delayed rule, but restart occurred within 3 seconds")
	}

	// Wait for restart to occur (should be delayed by 6 seconds)
	t.Log("Waiting for delayed restart to occur...")
	err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 15*time.Second, true, func(ctx context.Context) (bool, error) {
		annotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)

		return annotation != initialAnnotation, nil
	})
	if err != nil {
		t.Fatalf("Restart did not happen after delay period: %v", err)
	}

	t.Log("SUCCESS: Delayed rule took precedence over no-delay rule")
}

// TestDelayedRestartAlreadyDelayedTarget tests behavior when a target is already delayed
func TestDelayedRestartAlreadyDelayedTarget(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	namespace := generateTestNamespace(namespacePrefix)
	setupDelayTestEnvironment(ctx, t, clients, namespace)
	defer cleanupDelayTest(ctx, t, clients, namespace)

	t.Log("=== Testing already delayed target (only one restart should occur) ===")

	// Create deployment
	t.Log("Creating test deployment...")
	deployment := createDelayTestDeployment(namespace, "delay-already-deployment")
	if err := clients.k8sClient.Create(ctx, deployment); err != nil {
		t.Fatalf("Failed to create Deployment: %v", err)
	}

	if err := waitForDeploymentReady(ctx, clients.clientset, namespace, deployment.Name); err != nil {
		t.Fatalf("Deployment did not become ready: %v", err)
	}

	// Create ConfigMap
	t.Log("Creating test ConfigMap...")
	configMap := createDelayTestConfigMap(namespace, "delay-already-config")
	if err := clients.k8sClient.Create(ctx, configMap); err != nil {
		t.Fatalf("Failed to create ConfigMap: %v", err)
	}

	// Create RestartRule with 8 second delay
	t.Log("Creating RestartRule with 8 second delay...")
	restartRule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "delay-already-rule",
			Namespace: namespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			DelayRestart: int32Ptr(8),
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "ConfigMap",
					Name:       configMap.Name,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: deployment.Name,
				},
			},
		},
	}
	if err := clients.k8sClient.Create(ctx, restartRule); err != nil {
		t.Fatalf("Failed to create RestartRule: %v", err)
	}

	t.Log("Waiting for RestartRule to become Active...")
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, namespace, restartRule.Name); err != nil {
		t.Fatalf("RestartRule did not become ready: %v", err)
	}

	// Get initial restart annotation
	initialAnnotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)

	// Update ConfigMap first time
	t.Log("Updating ConfigMap first time to trigger delayed restart...")
	if err := updateDelayTestConfigMapContent(ctx, clients.k8sClient, namespace, configMap.Name); err != nil {
		t.Fatalf("Failed to update ConfigMap first time: %v", err)
	}

	// Wait 3 seconds and update ConfigMap again
	time.Sleep(3 * time.Second)
	t.Log("Updating ConfigMap second time while restart is still delayed...")
	if err := updateDelayTestConfigMapContent(ctx, clients.k8sClient, namespace, configMap.Name); err != nil {
		t.Fatalf("Failed to update ConfigMap second time: %v", err)
	}

	// Wait for restart to occur
	t.Log("Waiting for delayed restart to occur...")
	err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 20*time.Second, true, func(ctx context.Context) (bool, error) {
		annotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)

		return annotation != initialAnnotation, nil
	})
	if err != nil {
		t.Fatalf("Restart did not happen after delay period: %v", err)
	}

	firstRestartAnnotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)
	t.Logf("First restart occurred with annotation: %s", firstRestartAnnotation)

	// Wait a bit longer to ensure no second restart occurs
	t.Log("Verifying no second restart occurs...")
	time.Sleep(10 * time.Second)
	finalAnnotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)
	if finalAnnotation != firstRestartAnnotation {
		t.Errorf("Second restart occurred unexpectedly! Expected only one restart, but found two different annotations")
	}

	t.Log("SUCCESS: Only one restart occurred despite multiple ConfigMap updates")
}

//nolint:dupl // Similar test patterns expected for StatefulSet testing
func TestDelayedRestartStatefulSet(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	namespace := generateTestNamespace(namespacePrefix)
	setupDelayTestEnvironment(ctx, t, clients, namespace)
	defer cleanupDelayTest(ctx, t, clients, namespace)

	t.Log("=== Testing delayed restart with StatefulSet ===")

	// Create StatefulSet
	t.Log("Creating test StatefulSet...")
	statefulSet := createDelayTestStatefulSet(namespace, "delay-statefulset")
	if err := clients.k8sClient.Create(ctx, statefulSet); err != nil {
		t.Fatalf("Failed to create StatefulSet: %v", err)
	}

	if err := waitForStatefulSetReady(ctx, clients.k8sClient, namespace, statefulSet.Name); err != nil {
		t.Fatalf("StatefulSet did not become ready: %v", err)
	}

	// Create Secret
	t.Log("Creating test Secret...")
	secret := createDelayTestSecret(namespace, "delay-statefulset-secret")
	if err := clients.k8sClient.Create(ctx, secret); err != nil {
		t.Fatalf("Failed to create Secret: %v", err)
	}

	// Create RestartRule with 5 second delay
	t.Log("Creating RestartRule with 5 second delay for StatefulSet...")
	restartRule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "delay-statefulset-rule",
			Namespace: namespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			DelayRestart: int32Ptr(5),
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "Secret",
					Name:       secret.Name,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "StatefulSet",
					Name: statefulSet.Name,
				},
			},
		},
	}
	if err := clients.k8sClient.Create(ctx, restartRule); err != nil {
		t.Fatalf("Failed to create RestartRule: %v", err)
	}

	t.Log("Waiting for RestartRule to become Active...")
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, namespace, restartRule.Name); err != nil {
		t.Fatalf("RestartRule did not become ready: %v", err)
	}

	// Get initial restart annotation
	initialAnnotation := getStatefulSetRestartAnnotation(ctx, t, clients.k8sClient, namespace, statefulSet.Name)

	// Update Secret to trigger restart
	t.Log("Updating Secret to trigger delayed restart...")
	if err := updateDelayTestSecretContent(ctx, clients.k8sClient, namespace, secret.Name); err != nil {
		t.Fatalf("Failed to update Secret: %v", err)
	}

	// Verify restart hasn't happened immediately (within 2 seconds)
	t.Log("Verifying restart doesn't happen immediately...")
	time.Sleep(2 * time.Second)
	annotation := getStatefulSetRestartAnnotation(ctx, t, clients.k8sClient, namespace, statefulSet.Name)
	if annotation != initialAnnotation {
		t.Errorf("StatefulSet restart happened too early! Expected delay of 5 seconds, but restart occurred within 2 seconds")
	}

	// Wait for delay to complete and verify restart happened
	t.Log("Waiting for delayed restart to occur...")
	err := wait.PollUntilContextTimeout(ctx, 1*time.Second, 15*time.Second, true, func(ctx context.Context) (bool, error) {
		annotation := getStatefulSetRestartAnnotation(ctx, t, clients.k8sClient, namespace, statefulSet.Name)

		return annotation != initialAnnotation, nil
	})
	if err != nil {
		t.Fatalf("StatefulSet restart did not happen after delay period: %v", err)
	}

	t.Log("SUCCESS: StatefulSet was restarted after 5 second delay")
}

// TODO: Use this tests to introduce a general test case refactoring based on a builder pattern
func TestMinimumDelay(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	clients := setupTestClients(t)

	opts := manager.DefaultSetupOptions()
	opts.MinimumDelay = 10 // Set minimum delay to 10 seconds for testing

	namespace := generateTestNamespace(namespacePrefix)
	setupDelayTestEnvironment(ctx, t, clients, namespace, *opts)
	defer cleanupDelayTest(ctx, t, clients, namespace)

	tests := []struct {
		name           string
		ruleName       string
		expected       string
		configMapName  string
		deploymentName string
	}{
		{
			name:           "10 seconds minimum delay, no delay specified",
			ruleName:       "rule-minimum-10",
			configMapName:  "configmap-minimum-10",
			deploymentName: "deployment-minimum-10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deployment := createDelayTestDeployment(namespace, tt.deploymentName)
			if err := clients.k8sClient.Create(ctx, deployment); err != nil {
				t.Fatalf("Failed to create Deployment: %v", err)
			}

			restartRule := createConfigMapRule(tt.ruleName, tt.configMapName, tt.deploymentName, namespace)
			if err := clients.k8sClient.Create(ctx, restartRule); err != nil {
				t.Fatalf("Failed to create RestartRule: %v", err)
			}

			configMap := createDelayTestConfigMap(namespace, tt.configMapName)
			if err := clients.k8sClient.Create(ctx, configMap); err != nil {
				t.Fatalf("Failed to create ConfigMap: %v", err)
			}

			if err := waitForDeploymentReady(ctx, clients.clientset, namespace, deployment.Name); err != nil {
				t.Fatalf("Deployment did not become ready: %v", err)
			}

			if err := waitForRestartRuleReady(ctx, clients.k8sClient, namespace, restartRule.Name); err != nil {
				t.Fatalf("RestartRule did not become ready: %v", err)
			}

			initialAnnotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)

			if err := updateDelayTestConfigMapContent(ctx, clients.k8sClient, namespace, configMap.Name); err != nil {
				t.Fatalf("Failed to update ConfigMap: %v", err)
			}

			time.Sleep(5 * time.Second)
			annotation := getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)
			if annotation != initialAnnotation {
				t.Errorf("Restart happened too early! Expected minimum delay of 10 seconds, but restart occurred within 5 seconds")
			}

			time.Sleep(6 * time.Second)
			annotation = getRestartAnnotation(ctx, t, clients.k8sClient, namespace, deployment.Name)
			if annotation == initialAnnotation {
				t.Errorf("No restart occurred after minimum delay period")
			}

			logs := clients.controllerManager.GetLogs()
			logAllLines(t, logs)

		})
	}
}

// Helper functions

func setupDelayTestEnvironment(ctx context.Context, t *testing.T, clients *testClients, namespace string,
	options ...manager.SetupOptions) {
	t.Log("Creating delay test namespace...")
	if err := createNamespace(ctx, clients.clientset, namespace); err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}

	t.Log("Starting controller manager...")
	var opts manager.SetupOptions
	if len(options) == 0 {
		opts = *manager.DefaultSetupOptions()
	} else {
		opts = options[0]
	}

	if err := clients.controllerManager.Start(ctx, opts); err != nil {
		t.Fatalf("Failed to start controller manager: %v", err)
	}
}

func cleanupDelayTest(ctx context.Context, t *testing.T, clients *testClients, namespace string) {
	t.Log("Cleaning up delay test resources...")

	// Stop controller manager first
	if err := clients.controllerManager.Stop(); err != nil {
		t.Logf("Error stopping controller manager: %v", err)
	}

	// Clean up namespace
	if err := clients.clientset.CoreV1().Namespaces().Delete(
		ctx, namespace, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		t.Logf("Failed to delete delay test namespace: %v", err)
	}
}

func createDelayTestDeployment(namespace, name string) *appsv1.Deployment {
	replicas := int32(1)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "delay-test",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":        "delay-test",
					"deployment": name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":        "delay-test",
						"deployment": name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "nginx:1.21-alpine",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 80,
									Protocol:      corev1.ProtocolTCP,
								},
							},
						},
					},
				},
			},
		},
	}
}

func createDelayTestStatefulSet(namespace, name string) *appsv1.StatefulSet {
	replicas := int32(1)

	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "delay-test",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: name,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":         "delay-test",
					"statefulset": name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":         "delay-test",
						"statefulset": name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "nginx:1.21-alpine",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 80,
									Protocol:      corev1.ProtocolTCP,
								},
							},
						},
					},
				},
			},
		},
	}
}

func createDelayTestConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "delay-test",
			},
		},
		Data: map[string]string{
			"config": "initial-config-value",
		},
	}
}

func createDelayTestSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "delay-test",
			},
		},
		Data: map[string][]byte{
			"api-key": []byte("initial-secret-value"),
		},
	}
}

func updateDelayTestConfigMapContent(ctx context.Context, k8sClient client.Client, namespace, name string) error {
	updatedConfigMap := &corev1.ConfigMap{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: namespace, Name: name}, updatedConfigMap); err != nil {
		return err
	}

	// Add timestamp to ensure the update is detected
	updatedConfigMap.Data["config"] = "updated-config-value-" + time.Now().Format("20060102-150405.000")

	return k8sClient.Update(ctx, updatedConfigMap)
}

func updateDelayTestSecretContent(ctx context.Context, k8sClient client.Client, namespace, name string) error {
	updatedSecret := &corev1.Secret{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: namespace, Name: name}, updatedSecret); err != nil {
		return err
	}

	// Add timestamp to ensure the update is detected
	updatedSecret.Data["api-key"] = []byte("updated-secret-value-" + time.Now().Format("20060102-150405.000"))

	return k8sClient.Update(ctx, updatedSecret)
}

func getRestartAnnotation(ctx context.Context, t *testing.T, k8sClient client.Client, namespace, name string) string {
	deployment := &appsv1.Deployment{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: namespace, Name: name}, deployment); err != nil {
		t.Fatalf("Failed to get Deployment: %v", err)
	}

	if annotations := deployment.Spec.Template.Annotations; annotations != nil {
		return annotations[restartAnnotation]
	}

	return ""
}

//nolint:lll // Function signature length acceptable for clarity
func getStatefulSetRestartAnnotation(ctx context.Context, t *testing.T, k8sClient client.Client, namespace, name string) string {
	statefulSet := &appsv1.StatefulSet{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: namespace, Name: name}, statefulSet); err != nil {
		t.Fatalf("Failed to get StatefulSet: %v", err)
	}

	if annotations := statefulSet.Spec.Template.Annotations; annotations != nil {
		return annotations[restartAnnotation]
	}

	return ""
}

func waitForStatefulSetReady(ctx context.Context, k8sClient client.Client, namespace, name string) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		statefulSet := &appsv1.StatefulSet{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, statefulSet); err != nil {
			return false, err
		}

		// Check if StatefulSet is ready by verifying replicas
		if statefulSet.Status.ReadyReplicas == *statefulSet.Spec.Replicas &&
			statefulSet.Status.Replicas == *statefulSet.Spec.Replicas &&
			statefulSet.Status.UpdatedReplicas == *statefulSet.Spec.Replicas {
			return true, nil
		}

		return false, nil
	})
}
