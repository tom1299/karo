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
	delayTestNamespace   = "default"
	delayConfigMapName   = "delay-test-config"
	delayDeploymentName  = "delay-test-app"
	delayRestartRuleName = "delay-test-rule"
	testDelayDuration    = 3 * time.Second
	delayTolerance       = 5 * time.Second
)

func TestDelayedRestartE2E(t *testing.T) {
	ctx := context.Background()
	clients := setupTestClients(t)

	setupDelayTestEnvironment(ctx, t, clients)
	defer cleanupDelayTest(ctx, t, clients)

	// Test delayed restart functionality
	testDelayedRestart(ctx, t, clients)
}

func setupDelayTestEnvironment(ctx context.Context, t *testing.T, clients *testClients) {
	t.Helper()

	// Start controller manager
	t.Log("Starting controller manager...")
	if err := clients.controllerManager.Start(ctx); err != nil {
		t.Fatalf("Failed to start controller manager: %v", err)
	}

	// Create initial ConfigMap first (before deployment that references it)
	t.Log("Creating ConfigMap...")
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      delayConfigMapName,
			Namespace: delayTestNamespace,
		},
		Data: map[string]string{
			"nginx.conf": `server {
    listen 80;
    server_name localhost;
    location / {
        root /usr/share/nginx/html;
        index index.html;
    }
}`,
		},
	}

	if err := clients.k8sClient.Create(ctx, configMap); err != nil {
		t.Fatalf("Failed to create test ConfigMap: %v", err)
	}
	t.Log("ConfigMap created successfully")

	// Create test deployment
	t.Log("Creating deployment...")
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      delayDeploymentName,
			Namespace: delayTestNamespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: delayInt32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": delayDeploymentName,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": delayDeploymentName,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 80,
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "config-volume",
									MountPath: "/etc/nginx/conf.d",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "config-volume",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: delayConfigMapName,
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if err := clients.k8sClient.Create(ctx, deployment); err != nil {
		t.Fatalf("Failed to create test deployment: %v", err)
	}
	t.Log("Deployment created successfully")

	// Wait for deployment to be ready
	t.Log("Waiting for deployment to be ready...")
	waitForDelayDeploymentReady(ctx, t, clients, delayDeploymentName, delayTestNamespace)
	t.Log("Deployment is ready")

	// Create RestartRule with delay
	t.Log("Creating RestartRule with delay...")
	delayDuration := metav1.Duration{Duration: testDelayDuration}
	restartRule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      delayRestartRuleName,
			Namespace: delayTestNamespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			DelayRestart: &delayDuration,
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind: "ConfigMap",
					Name: delayConfigMapName,
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: delayDeploymentName,
				},
			},
		},
	}

	if err := clients.k8sClient.Create(ctx, restartRule); err != nil {
		t.Fatalf("Failed to create RestartRule: %v", err)
	}
	t.Log("RestartRule created successfully")

	// Wait for RestartRule to be processed
	t.Log("Waiting for RestartRule to be processed...")
	time.Sleep(2 * time.Second)
}

func testDelayedRestart(ctx context.Context, t *testing.T, clients *testClients) {
	// Get initial restart time
	t.Log("Getting initial deployment restart time...")
	initialRestartedAt := getDeploymentRestartTime(ctx, t, clients, delayDeploymentName, delayTestNamespace)
	t.Logf("Initial restart time: %s", initialRestartedAt)

	// Record the time when we trigger the change
	changeTime := time.Now()
	t.Logf("Triggering ConfigMap change at: %s", changeTime.Format(time.RFC3339))

	// Update ConfigMap to trigger restart
	t.Log("Updating ConfigMap to trigger restart...")
	configMap := &corev1.ConfigMap{}
	if err := clients.k8sClient.Get(ctx, client.ObjectKey{
		Name:      delayConfigMapName,
		Namespace: delayTestNamespace,
	}, configMap); err != nil {
		t.Fatalf("Failed to get ConfigMap: %v", err)
	}

	configMap.Data["nginx.conf"] = `server {
    listen 80;
    server_name localhost;
    location / {
        root /usr/share/nginx/html;
        index index.html index.htm;
    }
    # Updated configuration to trigger restart
}`

	if err := clients.k8sClient.Update(ctx, configMap); err != nil {
		t.Fatalf("Failed to update ConfigMap: %v", err)
	}

	// Give a moment for the controller to process the change
	time.Sleep(2 * time.Second)

	// Check controller logs to see what happened
	logs := clients.controllerManager.GetRecentLogs(20)
	t.Log("Controller logs after ConfigMap update:")
	for _, logLine := range logs {
		t.Logf("  %s", logLine)
	}
	earlyCheckRestartTime := getDeploymentRestartTime(ctx, t, clients, delayDeploymentName, delayTestNamespace)
	if earlyCheckRestartTime != initialRestartedAt {
		t.Errorf("Deployment was restarted too early. Expected delay of %v", testDelayDuration)
	}

	// Wait for the delay period and then check that the deployment is restarted
	waitTime := testDelayDuration + delayTolerance
	t.Logf("Waiting %v for delayed restart to execute (delay: %v, tolerance: %v)", waitTime, testDelayDuration, delayTolerance)

	err := wait.PollImmediate(2*time.Second, waitTime, func() (bool, error) {
		currentRestartTime := getDeploymentRestartTime(ctx, t, clients, delayDeploymentName, delayTestNamespace)
		return currentRestartTime != initialRestartedAt, nil
	})

	if err != nil {
		t.Fatalf("Delayed restart did not execute within expected time: %v", err)
	}

	// Verify the restart happened after the expected delay
	finalRestartTime := getDeploymentRestartTime(ctx, t, clients, delayDeploymentName, delayTestNamespace)
	restartTime, err := time.Parse(time.RFC3339, finalRestartTime)
	if err != nil {
		t.Fatalf("Failed to parse restart time: %v", err)
	}

	actualDelay := restartTime.Sub(changeTime)
	if actualDelay < testDelayDuration-delayTolerance {
		t.Errorf("Restart happened too early. Expected delay: %v, Actual delay: %v", testDelayDuration, actualDelay)
	}
	if actualDelay > testDelayDuration+delayTolerance {
		t.Errorf("Restart happened too late. Expected delay: %v, Actual delay: %v", testDelayDuration, actualDelay)
	}

	t.Logf("Delayed restart executed successfully with delay: %v", actualDelay)

	// Verify the RestartRule status shows the delayed restart
	restartRule := &karov1alpha1.RestartRule{}
	if err := clients.k8sClient.Get(ctx, client.ObjectKey{
		Name:      delayRestartRuleName,
		Namespace: delayTestNamespace,
	}, restartRule); err != nil {
		t.Fatalf("Failed to get RestartRule: %v", err)
	}

	// Check for "Delayed" status in restart history
	found := false
	for _, event := range restartRule.Status.RestartHistory {
		if event.Status == "Delayed" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected to find 'Delayed' status in RestartRule history")
	}
}

func cleanupDelayTest(ctx context.Context, t *testing.T, clients *testClients) {
	t.Helper()

	// Stop controller manager
	clients.controllerManager.Stop()

	// Delete RestartRule
	restartRule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      delayRestartRuleName,
			Namespace: delayTestNamespace,
		},
	}
	if err := clients.k8sClient.Delete(ctx, restartRule); err != nil {
		t.Logf("Failed to delete RestartRule (may not exist): %v", err)
	}

	// Delete ConfigMap
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      delayConfigMapName,
			Namespace: delayTestNamespace,
		},
	}
	if err := clients.k8sClient.Delete(ctx, configMap); err != nil {
		t.Logf("Failed to delete ConfigMap (may not exist): %v", err)
	}

	// Delete Deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      delayDeploymentName,
			Namespace: delayTestNamespace,
		},
	}
	if err := clients.k8sClient.Delete(ctx, deployment); err != nil {
		t.Logf("Failed to delete Deployment (may not exist): %v", err)
	}
}

func getDeploymentRestartTime(ctx context.Context, t *testing.T, clients *testClients, name, namespace string) string {
	t.Helper()

	deployment := &appsv1.Deployment{}
	if err := clients.k8sClient.Get(ctx, client.ObjectKey{
		Name:      name,
		Namespace: namespace,
	}, deployment); err != nil {
		t.Fatalf("Failed to get deployment: %v", err)
	}

	return deployment.Spec.Template.Annotations["karo.jeeatwork.com/restartedAt"]
}

func waitForDelayDeploymentReady(ctx context.Context, t *testing.T, clients *testClients, name, namespace string) {
	t.Helper()

	err := wait.PollImmediate(2*time.Second, 60*time.Second, func() (bool, error) {
		deployment := &appsv1.Deployment{}
		if err := clients.k8sClient.Get(ctx, client.ObjectKey{
			Name:      name,
			Namespace: namespace,
		}, deployment); err != nil {
			return false, err
		}

		return deployment.Status.ReadyReplicas == *deployment.Spec.Replicas, nil
	})

	if err != nil {
		t.Fatalf("Deployment %s/%s did not become ready: %v", namespace, name, err)
	}
}

func delayInt32Ptr(i int32) *int32 {
	return &i
}
