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
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

func TestRestartRuleE2E(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	setupTestEnvironment(ctx, t, clients)
	defer cleanup(ctx, t, clients.clientset, clients.k8sClient)

	configMapRestartTime := testConfigMapRestart(ctx, t, clients)
	secretRestartTime := testSecretRestart(ctx, t, clients, configMapRestartTime)

	validateTestResults(t, configMapRestartTime, secretRestartTime)
}

type testClients struct {
	clientset *kubernetes.Clientset
	k8sClient client.Client
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
		clientset: clientset,
		k8sClient: k8sClient,
	}
}

func setupTestEnvironment(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Creating test namespace...")
	if err := createNamespace(ctx, clients.clientset, testNamespace); err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
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
	time.Sleep(5 * time.Second)

	t.Log("Updating ConfigMap to trigger restart...")
	if err := updateConfigMapContent(ctx, clients.k8sClient); err != nil {
		t.Fatalf("Failed to update ConfigMap: %v", err)
	}

	restartTime := waitForDeploymentRestart(ctx, t, clients.k8sClient, "", "ConfigMap")
	t.Log("SUCCESS: Deployment was successfully rolled after ConfigMap update")

	return restartTime
}

func testSecretRestart(ctx context.Context, t *testing.T, clients *testClients, previousRestartTime string) string {
	t.Log("=== Testing Secret restart functionality ===")

	t.Log("Creating RestartRule for Secret...")
	secretRestartRule := createSecretRestartRule()
	if err := clients.k8sClient.Create(ctx, secretRestartRule); err != nil {
		t.Fatalf("Failed to create Secret RestartRule: %v", err)
	}
	time.Sleep(5 * time.Second)

	t.Log("Updating Secret to trigger restart...")
	if err := updateSecretContent(ctx, clients.k8sClient); err != nil {
		t.Fatalf("Failed to update Secret: %v", err)
	}

	restartTime := waitForDeploymentRestart(ctx, t, clients.k8sClient, previousRestartTime, "Secret")
	t.Log("SUCCESS: Deployment was successfully rolled after Secret update")

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

		restartAnnotation := "karo.jeeatwork.com/restartedAt"
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
