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
	// Setup kubernetes clients
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

	ctx := context.Background()

	// Create test namespace
	t.Log("Creating test namespace...")
	if err := createNamespace(ctx, clientset, testNamespace); err != nil {
		t.Fatalf("Failed to create namespace: %v", err)
	}
	defer cleanup(ctx, t, clientset, k8sClient)

	// Create ConfigMap
	t.Log("Creating nginx ConfigMap...")
	configMap := createNginxConfigMap()
	if err := k8sClient.Create(ctx, configMap); err != nil {
		t.Fatalf("Failed to create ConfigMap: %v", err)
	}

	// Create Secret
	t.Log("Creating nginx Secret...")
	secret := createNginxSecret()
	if err := k8sClient.Create(ctx, secret); err != nil {
		t.Fatalf("Failed to create Secret: %v", err)
	}

	// Create nginx Deployment
	t.Log("Creating nginx Deployment...")
	deployment := createNginxDeployment()
	if err := k8sClient.Create(ctx, deployment); err != nil {
		t.Fatalf("Failed to create Deployment: %v", err)
	}

	// Wait for deployment to be ready
	t.Log("Waiting for nginx Deployment to be ready...")
	if err := waitForDeploymentReady(ctx, clientset, testNamespace, deploymentName); err != nil {
		t.Fatalf("Deployment did not become ready: %v", err)
	}

	// Create RestartRule for ConfigMap
	t.Log("Creating RestartRule for ConfigMap...")
	restartRule := createRestartRule()
	if err := k8sClient.Create(ctx, restartRule); err != nil {
		t.Fatalf("Failed to create RestartRule: %v", err)
	}

	// Wait a bit for the controller to process the RestartRule
	time.Sleep(5 * time.Second)

	// Get initial deployment generation for comparison
	initialDeployment := &appsv1.Deployment{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: testNamespace, Name: deploymentName}, initialDeployment); err != nil {
		t.Fatalf("Failed to get initial deployment: %v", err)
	}
	initialGeneration := initialDeployment.Generation
	t.Logf("Initial deployment generation: %d", initialGeneration)

	// ======== CONFIGMAP TEST SECTION ========
	t.Log("=== Testing ConfigMap restart functionality ===")

	// Update ConfigMap to trigger restart
	t.Log("Updating ConfigMap to trigger restart...")
	updatedConfigMap := &corev1.ConfigMap{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: testNamespace, Name: configMapName}, updatedConfigMap); err != nil {
		t.Fatalf("Failed to get ConfigMap for update: %v", err)
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

	if err := k8sClient.Update(ctx, updatedConfigMap); err != nil {
		t.Fatalf("Failed to update ConfigMap: %v", err)
	}

	// Check if deployment was rolled (annotation should be present)
	t.Log("Checking if deployment was rolled after ConfigMap update...")
	var configMapRestartTime string
	err = wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		currentDeployment := &appsv1.Deployment{}
		if err := k8sClient.Get(
			ctx, client.ObjectKey{Namespace: testNamespace, Name: deploymentName}, currentDeployment); err != nil {
			return false, err
		}

		// Check if the restart annotation is present
		restartAnnotation := "karo.jeeatwork.com/restartedAt"
		if annotations := currentDeployment.Spec.Template.Annotations; annotations != nil {
			if restartTime, exists := annotations[restartAnnotation]; exists {
				t.Logf("Found restart annotation after ConfigMap update: %s = %s", restartAnnotation, restartTime)
				configMapRestartTime = restartTime
				return true, nil
			}
		}

		t.Logf("Restart annotation not found after ConfigMap update, current annotations: %v",
			currentDeployment.Spec.Template.Annotations)
		return false, nil
	})

	if err != nil {
		t.Fatalf("Error while waiting for deployment roll after ConfigMap update: %v", err)
	}

	if configMapRestartTime == "" {
		t.Fatal("Deployment was not rolled after ConfigMap update")
	}

	t.Log("SUCCESS: Deployment was successfully rolled after ConfigMap update")

	// ======== SECRET TEST SECTION ========
	t.Log("=== Testing Secret restart functionality ===")

	// Create RestartRule for Secret
	t.Log("Creating RestartRule for Secret...")
	secretRestartRule := createSecretRestartRule()
	if err := k8sClient.Create(ctx, secretRestartRule); err != nil {
		t.Fatalf("Failed to create Secret RestartRule: %v", err)
	}

	// Wait a bit for the controller to process the Secret RestartRule
	time.Sleep(5 * time.Second)

	// Update Secret to trigger restart
	t.Log("Updating Secret to trigger restart...")
	updatedSecret := &corev1.Secret{}
	if err := k8sClient.Get(
		ctx, client.ObjectKey{Namespace: testNamespace, Name: secretName}, updatedSecret); err != nil {
		t.Fatalf("Failed to get Secret for update: %v", err)
	}

	updatedSecret.Data["api-key"] = []byte("updated-secret-key-456")
	updatedSecret.Data["config"] = []byte("debug=true\nlog_level=debug")

	if err := k8sClient.Update(ctx, updatedSecret); err != nil {
		t.Fatalf("Failed to update Secret: %v", err)
	}

	// Check if deployment was rolled again (annotation should be updated)
	t.Log("Checking if deployment was rolled after Secret update...")
	var secretRestartTime string
	err = wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		currentDeployment := &appsv1.Deployment{}
		if err := k8sClient.Get(
			ctx, client.ObjectKey{Namespace: testNamespace, Name: deploymentName}, currentDeployment); err != nil {
			return false, err
		}

		// Check if the restart annotation is present and different from ConfigMap restart
		restartAnnotation := "karo.jeeatwork.com/restartedAt"
		if annotations := currentDeployment.Spec.Template.Annotations; annotations != nil {
			if restartTime, exists := annotations[restartAnnotation]; exists {
				// The restart time should be different from the ConfigMap restart time
				if restartTime != configMapRestartTime {
					t.Logf("Found updated restart annotation after Secret update: %s = %s", restartAnnotation, restartTime)
					secretRestartTime = restartTime
					return true, nil
				} else {
					t.Logf("Restart annotation exists but hasn't changed since ConfigMap update: %s", restartTime)
				}
			}
		}

		t.Logf("Restart annotation not found or not updated after Secret update, current annotations: %v",
			currentDeployment.Spec.Template.Annotations)
		return false, nil
	})

	if err != nil {
		t.Fatalf("Error while waiting for deployment roll after Secret update: %v", err)
	}

	if secretRestartTime == "" {
		t.Fatal("Deployment was not rolled after Secret update")
	}

	t.Log("SUCCESS: Deployment was successfully rolled after Secret update")

	// ======== FINAL VALIDATION ========
	t.Logf("=== Test completed successfully ===")
	t.Logf("ConfigMap restart time: %s", configMapRestartTime)
	t.Logf("Secret restart time: %s", secretRestartTime)
	t.Log("Both ConfigMap and Secret updates successfully triggered deployment restarts")
}
