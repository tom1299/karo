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
	"k8s.io/client-go/kubernetes"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const restartAnnotation = "karo.jeeatwork.com/restartedAt"

func TestRestartRuleE2E(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	setupTestEnvironment(ctx, t, clients)
	defer cleanup(ctx, t, clients)

	configMapRestartTime := testConfigMapRestart(ctx, t, clients)
	secretRestartTime := testSecretRestart(ctx, t, clients, configMapRestartTime)

	validateTestResults(t, configMapRestartTime, secretRestartTime)
}

func setupTestClients(t *testing.T) *testClients {
	cfg, err := config.GetConfig()
	if err != nil {
		t.Fatalf("Failed to get kubernetes config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("Failed to create kubernetes clientset: %v", err)
	}

	scheme := setupScheme()
	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("Failed to create controller-runtime client: %v", err)
	}

	return &testClients{
		clientset:         clientset,
		k8sClient:         k8sClient,
		controllerManager: NewControllerManager(t),
	}
}

func setupTestEnvironment(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Creating test namespace...")
	if err := createNamespace(ctx, clients.clientset, testNamespace); err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}

	t.Log("Starting controller manager...")
	if err := clients.controllerManager.Start(ctx); err != nil {
		t.Fatalf("Failed to start controller manager: %v", err)
	}

	t.Log("Creating nginx ConfigMap...")
	configMap := createNginxConfigMap()
	if err := clients.k8sClient.Create(ctx, configMap); err != nil {
		t.Fatalf("Failed to create ConfigMap: %v", err)
	}

	t.Log("Creating nginx Secret...")
	secret := createNginxSecret()
	if err := clients.k8sClient.Create(ctx, secret); err != nil {
		t.Fatalf("Failed to create Secret: %v", err)
	}

	t.Log("Creating nginx Deployment...")
	deployment := createNginxDeployment()
	if err := clients.k8sClient.Create(ctx, deployment); err != nil {
		t.Fatalf("Failed to create Deployment: %v", err)
	}

	t.Log("Waiting for nginx Deployment to be ready...")
	if err := waitForDeploymentReady(ctx, clients.clientset, testNamespace, deploymentName); err != nil {
		t.Fatalf("Deployment did not become ready: %v", err)
	}
}

func testConfigMapRestart(ctx context.Context, t *testing.T, clients *testClients) string {
	t.Log("=== Testing ConfigMap restart functionality ===")

	t.Log("Creating RestartRule for ConfigMap...")
	restartRule := createRestartRule()
	if err := clients.k8sClient.Create(ctx, restartRule); err != nil {
		t.Fatalf("Failed to create RestartRule: %v", err)
	}

	t.Log("Waiting for RestartRule to become Active...")
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, testNamespace, restartRuleName); err != nil {
		t.Fatalf("RestartRule did not become ready: %v", err)
	}

	t.Log("Updating ConfigMap to trigger restart...")
	if err := updateConfigMapContent(ctx, clients.k8sClient); err != nil {
		t.Fatalf("Failed to update ConfigMap: %v", err)
	}

	restartTime := waitForDeploymentRestart(ctx, t, clients.k8sClient, "", "ConfigMap")

	// Also check that restart event was recorded in the RestartRule status
	t.Log("Checking that restart event was recorded in RestartRule status...")
	if err := waitForRestartEvent(ctx, clients.k8sClient, testNamespace, restartRuleName, deploymentName); err != nil {
		t.Fatalf("Restart event was not recorded in RestartRule status: %v", err)
	}

	t.Log("SUCCESS: Deployment was successfully rolled after ConfigMap update and event was recorded")

	return restartTime
}

func testSecretRestart(ctx context.Context, t *testing.T, clients *testClients, previousRestartTime string) string {
	t.Log("=== Testing Secret restart functionality ===")

	t.Log("Creating RestartRule for Secret...")
	secretRestartRule := createSecretRestartRule()
	if err := clients.k8sClient.Create(ctx, secretRestartRule); err != nil {
		t.Fatalf("Failed to create Secret RestartRule: %v", err)
	}

	t.Log("Waiting for Secret RestartRule to become Active...")
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, testNamespace, "nginx-secret-restart-rule"); err != nil {
		t.Fatalf("Secret RestartRule did not become ready: %v", err)
	}

	t.Log("Updating Secret to trigger restart...")
	if err := updateSecretContent(ctx, clients.k8sClient); err != nil {
		t.Fatalf("Failed to update Secret: %v", err)
	}

	restartTime := waitForDeploymentRestart(ctx, t, clients.k8sClient, previousRestartTime, "Secret")

	// Also check that restart event was recorded in the RestartRule status
	t.Log("Checking that restart event was recorded in Secret RestartRule status...")
	if err := waitForRestartEvent(ctx, clients.k8sClient, testNamespace,
		"nginx-secret-restart-rule", deploymentName); err != nil {
		t.Fatalf("Restart event was not recorded in Secret RestartRule status: %v", err)
	}

	t.Log("SUCCESS: Deployment was successfully rolled after Secret update and event was recorded")

	return restartTime
}

func updateConfigMapContent(ctx context.Context, k8sClient client.Client) error {
	updatedConfigMap := &corev1.ConfigMap{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: testNamespace, Name: configMapName}, updatedConfigMap); err != nil {
		return err
	}

	updatedConfigMap.Data["nginx.conf"] = `
events {
    worker_connections 1024;
}
http {
    server {
        listen 80;
        location / {
            return 200 'Hello from updated nginx!';
            add_header Content-Type text/plain;
        }
    }
}`

	return k8sClient.Update(ctx, updatedConfigMap)
}

func updateSecretContent(ctx context.Context, k8sClient client.Client) error {
	updatedSecret := &corev1.Secret{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: testNamespace, Name: secretName}, updatedSecret); err != nil {
		return err
	}

	updatedSecret.Data["api-key"] = []byte("updated-secret-key-456")
	updatedSecret.Data["config"] = []byte("debug=true\nlog_level=debug")

	return k8sClient.Update(ctx, updatedSecret)
}

func waitForDeploymentRestart(
	ctx context.Context,
	t *testing.T,
	k8sClient client.Client,
	previousRestartTime, resourceType string,
) string {
	t.Logf("Checking if deployment was rolled after %s update...", resourceType)
	var restartTime string

	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		currentDeployment := &appsv1.Deployment{}
		if err := k8sClient.Get(
			ctx, client.ObjectKey{Namespace: testNamespace, Name: deploymentName}, currentDeployment); err != nil {
			return false, err
		}

		if annotations := currentDeployment.Spec.Template.Annotations; annotations != nil {
			if currentRestartTime, exists := annotations[restartAnnotation]; exists {
				if previousRestartTime == "" || currentRestartTime != previousRestartTime {
					t.Logf("Found restart annotation after %s update: %s = %s", resourceType, restartAnnotation, currentRestartTime)
					restartTime = currentRestartTime

					return true, nil
				}
				t.Logf("Restart annotation exists but hasn't changed since last update: %s", currentRestartTime)
			}
		}

		t.Logf("Restart annotation not found after %s update, current annotations: %v",
			resourceType, currentDeployment.Spec.Template.Annotations)

		return false, nil
	})

	if err != nil {
		t.Fatalf("Error while waiting for deployment roll after %s update: %v", resourceType, err)
	}

	if restartTime == "" {
		t.Fatalf("Deployment was not rolled after %s update", resourceType)
	}

	return restartTime
}

func validateTestResults(t *testing.T, configMapRestartTime, secretRestartTime string) {
	t.Logf("=== Test completed successfully ===")
	t.Logf("ConfigMap restart time: %s", configMapRestartTime)
	t.Logf("Secret restart time: %s", secretRestartTime)
	t.Log("Both ConfigMap and Secret updates successfully triggered deployment restarts")
}

func TestRestartRuleRegexE2E(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	setupTestEnvironmentForRegex(ctx, t, clients)
	defer cleanupRegexTest(ctx, t, clients)

	testRegexConfigMapRestart(ctx, t, clients)
	testRegexSecretRestart(ctx, t, clients)
	testRegexMultipleResources(ctx, t, clients)

	t.Log("=== Regex E2E tests completed successfully ===")
}

func setupTestEnvironmentForRegex(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Creating test namespace for regex tests...")
	if err := createNamespace(ctx, clients.clientset, regexTestNamespace); err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}

	t.Log("Starting controller manager...")
	if err := clients.controllerManager.Start(ctx); err != nil {
		t.Fatalf("Failed to start controller manager: %v", err)
	}

	setupRegexTestConfigMaps(ctx, t, clients)
	setupRegexTestSecrets(ctx, t, clients)
	setupRegexTestDeployments(ctx, t, clients)
}

func setupRegexTestConfigMaps(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Creating multiple ConfigMaps with different names...")
	configMaps := []*corev1.ConfigMap{
		createRegexTestConfigMap(regexTestNamespace, "frontend-nginx-app-config"),
		createRegexTestConfigMap(regexTestNamespace, "backend-nginx-api-config"),
		createRegexTestConfigMap(regexTestNamespace, "apache-web-config"),
	}

	for _, cm := range configMaps {
		if err := clients.k8sClient.Create(ctx, cm); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("Failed to create ConfigMap %s: %v", cm.Name, err)
		}
	}
}

func setupRegexTestSecrets(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Creating multiple Secrets with different names...")
	secrets := []*corev1.Secret{
		createRegexTestSecret(regexTestNamespace, "nginx-prod-secret"),
		createRegexTestSecret(regexTestNamespace, "nginx-dev-secret"),
		createRegexTestSecret(regexTestNamespace, "apache-secret"),
	}

	for _, secret := range secrets {
		if err := clients.k8sClient.Create(ctx, secret); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("Failed to create Secret %s: %v", secret.Name, err)
		}
	}
}

func setupRegexTestDeployments(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Creating test deployments...")
	deployments := []*appsv1.Deployment{
		createRegexTestDeployment(regexTestNamespace, "nginx-frontend"),
		createRegexTestDeployment(regexTestNamespace, "nginx-backend"),
	}

	for _, dep := range deployments {
		if err := clients.k8sClient.Create(ctx, dep); err != nil && !apierrors.IsAlreadyExists(err) {
			t.Fatalf("Failed to create Deployment %s: %v", dep.Name, err)
		}

		if err := waitForDeploymentReady(ctx, clients.clientset, regexTestNamespace, dep.Name); err != nil {
			t.Fatalf("Deployment %s did not become ready: %v", dep.Name, err)
		}
	}
}

func testRegexConfigMapRestart(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("=== Testing ConfigMap regex pattern matching ===")

	t.Log("Creating RestartRule with regex pattern for ConfigMaps...")
	restartRule := createRegexConfigMapRestartRule(regexTestNamespace)
	if err := clients.k8sClient.Create(ctx, restartRule); err != nil {
		t.Fatalf("Failed to create RestartRule: %v", err)
	}

	t.Log("Waiting for ConfigMap regex RestartRule to become Active...")
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, regexTestNamespace,
		"nginx-configmap-regex-rule"); err != nil {
		t.Fatalf("ConfigMap regex RestartRule did not become ready: %v", err)
	}

	t.Log("Updating ConfigMap that matches regex pattern...")
	err := updateRegexConfigMapContent(ctx, clients.k8sClient, regexTestNamespace, "frontend-nginx-app-config")
	if err != nil {
		t.Fatalf("Failed to update ConfigMap: %v", err)
	}

	checkDeploymentRestarted(ctx, t, clients.k8sClient, regexTestNamespace, "nginx-frontend", "ConfigMap regex")

	// Also check that restart event was recorded in the RestartRule status
	t.Log("Checking that restart event was recorded in regex ConfigMap RestartRule status...")
	if err := waitForRestartEvent(ctx, clients.k8sClient, regexTestNamespace,
		"nginx-configmap-regex-rule", "nginx-frontend"); err != nil {
		t.Fatalf("Restart event was not recorded in regex ConfigMap RestartRule status: %v", err)
	}

	t.Log("SUCCESS: Deployment was restarted after ConfigMap with matching regex was updated and event was recorded")
}

func testRegexSecretRestart(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("=== Testing Secret regex pattern matching ===")

	t.Log("Creating RestartRule with regex pattern for Secrets...")
	restartRule := createRegexSecretRestartRule(regexTestNamespace)
	if err := clients.k8sClient.Create(ctx, restartRule); err != nil {
		t.Fatalf("Failed to create RestartRule: %v", err)
	}

	t.Log("Waiting for Secret regex RestartRule to become Active...")
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, regexTestNamespace, "nginx-secret-regex-rule"); err != nil {
		t.Fatalf("Secret regex RestartRule did not become ready: %v", err)
	}

	t.Log("Updating Secret that matches regex pattern...")
	if err := updateRegexSecretContent(ctx, clients.k8sClient, regexTestNamespace, "nginx-prod-secret"); err != nil {
		t.Fatalf("Failed to update Secret: %v", err)
	}

	checkDeploymentRestarted(ctx, t, clients.k8sClient, regexTestNamespace, "nginx-frontend", "Secret regex")

	// Also check that restart event was recorded in the RestartRule status
	t.Log("Checking that restart event was recorded in regex Secret RestartRule status...")
	if err := waitForRestartEvent(ctx, clients.k8sClient, regexTestNamespace,
		"nginx-secret-regex-rule", "nginx-frontend"); err != nil {
		t.Fatalf("Restart event was not recorded in regex Secret RestartRule status: %v", err)
	}

	t.Log("SUCCESS: Deployment was restarted after Secret with matching regex was updated and event was recorded")
}

func testRegexMultipleResources(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("=== Testing that non-matching resources are not affected ===")

	// Create a separate RestartRule that should NOT match apache resources
	t.Log("Creating RestartRule for non-matching test...")
	nonMatchingRule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "apache-non-match-rule",
			Namespace: regexTestNamespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "ConfigMap",
					Name:       ".*nginx.*-config", // This should NOT match apache-web-config
					IsRegex:    true,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: "nginx-backend", // Target a different deployment
				},
			},
		},
	}

	if err := clients.k8sClient.Create(ctx, nonMatchingRule); err != nil {
		t.Fatalf("Failed to create non-matching RestartRule: %v", err)
	}

	t.Log("Waiting for non-matching RestartRule to become Active...")
	if err := waitForRestartRuleReady(ctx, clients.k8sClient, regexTestNamespace, "apache-non-match-rule"); err != nil {
		t.Fatalf("Non-matching RestartRule did not become ready: %v", err)
	}

	// Get current restart time for comparison
	currentDeployment := &appsv1.Deployment{}
	if err := clients.k8sClient.Get(
		ctx, client.ObjectKey{Namespace: regexTestNamespace, Name: "nginx-backend"}, currentDeployment); err != nil {
		t.Fatalf("Failed to get deployment: %v", err)
	}

	var previousRestartTime string
	if annotations := currentDeployment.Spec.Template.Annotations; annotations != nil {
		previousRestartTime = annotations[restartAnnotation]
	}

	t.Log("Updating ConfigMap that does NOT match regex pattern...")
	if err := updateRegexConfigMapContent(ctx, clients.k8sClient, regexTestNamespace, "apache-web-config"); err != nil {
		t.Fatalf("Failed to update ConfigMap: %v", err)
	}

	// Wait to ensure the controller has time to process (but no restart should happen)
	time.Sleep(10 * time.Second)

	// Verify deployment was NOT restarted
	updatedDeployment := &appsv1.Deployment{}
	if err := clients.k8sClient.Get(
		ctx, client.ObjectKey{Namespace: regexTestNamespace, Name: "nginx-backend"}, updatedDeployment); err != nil {
		t.Fatalf("Failed to get deployment: %v", err)
	}

	var currentRestartTime string
	if annotations := updatedDeployment.Spec.Template.Annotations; annotations != nil {
		currentRestartTime = annotations[restartAnnotation]
	}

	if currentRestartTime != previousRestartTime {
		t.Fatalf("Deployment was unexpectedly restarted after updating non-matching ConfigMap (previous: %s, current: %s)",
			previousRestartTime, currentRestartTime)
	}

	t.Log("SUCCESS: Deployment was NOT restarted after updating non-matching ConfigMap")
}

func checkDeploymentRestarted(
	ctx context.Context,
	t *testing.T,
	k8sClient client.Client,
	namespace, deploymentName, resourceType string,
) {
	t.Logf("Checking if deployment %s was restarted after %s update...", deploymentName, resourceType)

	err := wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		currentDeployment := &appsv1.Deployment{}
		if err := k8sClient.Get(
			ctx, client.ObjectKey{Namespace: namespace, Name: deploymentName}, currentDeployment); err != nil {
			return false, err
		}

		if annotations := currentDeployment.Spec.Template.Annotations; annotations != nil {
			if _, exists := annotations[restartAnnotation]; exists {
				t.Logf("Found restart annotation after %s update: %s = %s",
					resourceType, restartAnnotation, annotations[restartAnnotation])

				return true, nil
			}
		}

		return false, nil
	})

	if err != nil {
		t.Fatalf("Error while waiting for deployment restart after %s update: %v", resourceType, err)
	}
}

func updateRegexConfigMapContent(ctx context.Context, k8sClient client.Client, namespace, configMapName string) error {
	updatedConfigMap := &corev1.ConfigMap{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: namespace, Name: configMapName}, updatedConfigMap); err != nil {
		return err
	}

	updatedConfigMap.Data["config"] = "updated-config-data"

	return k8sClient.Update(ctx, updatedConfigMap)
}

func updateRegexSecretContent(ctx context.Context, k8sClient client.Client, namespace, secretName string) error {
	updatedSecret := &corev1.Secret{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: namespace, Name: secretName}, updatedSecret); err != nil {
		return err
	}

	updatedSecret.Data["api-key"] = []byte("updated-secret-data")

	return k8sClient.Update(ctx, updatedSecret)
}

func cleanupRegexTest(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Cleaning up regex test resources...")

	// Stop controller manager first
	if err := clients.controllerManager.Stop(); err != nil {
		t.Logf("Error stopping controller manager: %v", err)
	}

	// Clean up RestartRules
	restartRules := []*karov1alpha1.RestartRule{
		{ObjectMeta: metav1.ObjectMeta{Name: "nginx-configmap-regex-rule", Namespace: regexTestNamespace}},
		{ObjectMeta: metav1.ObjectMeta{Name: "nginx-secret-regex-rule", Namespace: regexTestNamespace}},
		{ObjectMeta: metav1.ObjectMeta{Name: "apache-non-match-rule", Namespace: regexTestNamespace}},
	}

	for _, rule := range restartRules {
		deleteResource(ctx, t, clients.k8sClient, rule, "RestartRule")
	}

	// Clean up other resources (deployments, configmaps, secrets)
	deployments := []string{"nginx-frontend", "nginx-backend"}
	for _, name := range deployments {
		deleteResource(ctx, t, clients.k8sClient, &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: regexTestNamespace}}, "Deployment")
	}

	configMaps := []string{"frontend-nginx-app-config", "backend-nginx-api-config", "apache-web-config"}
	for _, name := range configMaps {
		deleteResource(ctx, t, clients.k8sClient, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: regexTestNamespace}}, "ConfigMap")
	}

	secrets := []string{"nginx-prod-secret", "nginx-dev-secret", "apache-secret"}
	for _, name := range secrets {
		deleteResource(ctx, t, clients.k8sClient, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: regexTestNamespace}}, "Secret")
	}

	// Clean up namespace
	if err := clients.clientset.CoreV1().Namespaces().Delete(
		ctx, regexTestNamespace, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		t.Logf("Failed to delete regex test namespace: %v", err)
	}
}
