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

	// Create RestartRule
	t.Log("Creating RestartRule...")
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
	t.Log("Checking if deployment was rolled...")
	var deploymentRolled bool
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
				t.Logf("Found restart annotation: %s = %s", restartAnnotation, restartTime)
				deploymentRolled = true
				return true, nil
			}
		}

		t.Logf("Restart annotation not found, current annotations: %v",
			currentDeployment.Spec.Template.Annotations)
		return false, nil
	})

	if err != nil {
		t.Fatalf("Error while waiting for deployment roll: %v", err)
	}

	if !deploymentRolled {
		t.Fatal("Deployment was not rolled after ConfigMap update")
	}

	t.Log("SUCCESS: Deployment was successfully rolled after ConfigMap update")
}
