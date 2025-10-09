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
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"karo.jeeatwork.com/internal/manager"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	configMapName      = "nginx-config"
	secretName         = "nginx-secret"
	deploymentName     = "nginx"
	interval           = 10 * time.Second
	restartRuleName    = "nginx-restart-rule"
	testNamespace      = "karo-e2e-test"
	regexTestNamespace = "karo-regex-e2e-test"
	timeout            = 5 * time.Minute
)

var (
	ErrControllerAlreadyStarted = errors.New("controller manager is already started")
)

type testClients struct {
	clientset         *kubernetes.Clientset
	k8sClient         client.Client
	controllerManager *ControllerManager
}

// LogCapture captures logs from controller-runtime logger
type LogCapture struct {
	buffer *bytes.Buffer
	lines  []string
	mutex  sync.RWMutex
}

// NewLogCapture creates a new log capture instance
func NewLogCapture() *LogCapture {
	return &LogCapture{
		buffer: &bytes.Buffer{},
		lines:  make([]string, 0),
	}
}

// Write implements io.Writer to capture log output
func (lc *LogCapture) Write(p []byte) (n int, err error) {
	lc.mutex.Lock()
	defer lc.mutex.Unlock()

	// Write to buffer
	n, err = lc.buffer.Write(p)

	// Also store individual lines
	line := string(p)
	if line != "" {
		lc.lines = append(lc.lines, line)
	}

	return n, err
}

// GetLogs returns all captured log lines
func (lc *LogCapture) GetLogs() []string {
	lc.mutex.RLock()
	defer lc.mutex.RUnlock()

	logsCopy := make([]string, len(lc.lines))
	copy(logsCopy, lc.lines)

	return logsCopy
}

// GetRecentLogs returns the last n log lines
func (lc *LogCapture) GetRecentLogs(n int) []string {
	lc.mutex.RLock()
	defer lc.mutex.RUnlock()

	if len(lc.lines) <= n {
		logsCopy := make([]string, len(lc.lines))
		copy(logsCopy, lc.lines)

		return logsCopy
	}

	start := len(lc.lines) - n
	logsCopy := make([]string, n)
	copy(logsCopy, lc.lines[start:])

	return logsCopy
}

// ControllerManager manages the controller-runtime manager during e2e tests
type ControllerManager struct {
	manager    ctrl.Manager
	ctx        context.Context
	cancel     context.CancelFunc
	logCapture *LogCapture
	started    bool
	t          *testing.T
}

// NewControllerManager creates a new controller manager instance
func NewControllerManager(t *testing.T) *ControllerManager {
	return &ControllerManager{
		logCapture: NewLogCapture(),
		t:          t,
	}
}

// Start starts the controller manager in a goroutine
func (cm *ControllerManager) Start(ctx context.Context) error {
	if cm.started {
		return ErrControllerAlreadyStarted
	}

	// Setup logger to capture output
	logger := zap.New(zap.UseDevMode(true), zap.WriteTo(cm.logCapture))
	ctrl.SetLogger(logger)
	log.SetLogger(logger)

	// Setup manager with skip name validation to allow multiple controllers in tests
	opts := manager.DefaultSetupOptions()
	opts.SkipNameValidation = true

	var err error
	cm.manager, err = manager.SetupManager(opts)
	if err != nil {
		return fmt.Errorf("failed to setup manager: %w", err)
	}

	// Create context for manager
	cm.ctx, cm.cancel = context.WithCancel(ctx)

	// Start manager in goroutine
	go func() {
		cm.t.Log("Starting controller manager in goroutine...")
		if err := cm.manager.Start(cm.ctx); err != nil {
			// Only log error if it's not due to context cancellation
			if cm.ctx.Err() == nil {
				cm.t.Logf("Controller manager error: %v", err)
			}
		}
		cm.t.Log("Controller manager stopped")
	}()

	cm.started = true
	cm.t.Log("Controller manager started")

	// Wait a moment for the manager to start up
	time.Sleep(2 * time.Second)

	return nil
}

// Stop stops the controller manager
func (cm *ControllerManager) Stop() error {
	if !cm.started {
		return nil
	}

	cm.t.Log("Stopping controller manager...")

	// Cancel context to stop manager
	if cm.cancel != nil {
		cm.cancel()
	}

	// Wait a moment for graceful shutdown
	time.Sleep(1 * time.Second)

	cm.started = false

	return nil
}

// GetLogs returns all captured log lines
func (cm *ControllerManager) GetLogs() []string {
	return cm.logCapture.GetLogs()
}

// GetRecentLogs returns the last n log lines
func (cm *ControllerManager) GetRecentLogs(n int) []string {
	return cm.logCapture.GetRecentLogs(n)
}

func setupScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
	_ = karov1alpha1.AddToScheme(scheme)

	return scheme
}

func createNamespace(ctx context.Context, clientset *kubernetes.Clientset, name string) error {
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	_, err := clientset.CoreV1().Namespaces().Create(ctx, namespace, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}

	return nil
}

func createNginxConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: testNamespace,
			Labels: map[string]string{
				"app": "nginx",
			},
		},
		Data: map[string]string{
			"nginx.conf": `
events {
    worker_connections 1024;
}
http {
    server {
        listen 80;
        location / {
            return 200 'Hello from nginx!';
            add_header Content-Type text/plain;
        }
    }
}`,
		},
	}
}

func createNginxSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: testNamespace,
			Labels: map[string]string{
				"app": "nginx",
			},
		},
		Data: map[string][]byte{
			"api-key": []byte("initial-secret-key-123"),
			"config":  []byte("debug=false\nlog_level=info"),
		},
	}
}

func createNginxDeployment() *appsv1.Deployment {
	replicas := int32(1)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: testNamespace,
			Labels: map[string]string{
				"app": "nginx",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "nginx",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "nginx",
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:1.21-alpine",
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 80,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name: "API_KEY",
									ValueFrom: &corev1.EnvVarSource{
										SecretKeyRef: &corev1.SecretKeySelector{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: secretName,
											},
											Key: "api-key",
										},
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "nginx-config",
									MountPath: "/etc/nginx/nginx.conf",
									SubPath:   "nginx.conf",
								},
								{
									Name:      "nginx-secret-config",
									MountPath: "/etc/secret",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "nginx-config",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: configMapName,
									},
								},
							},
						},
						{
							Name: "nginx-secret-config",
							VolumeSource: corev1.VolumeSource{
								Secret: &corev1.SecretVolumeSource{
									SecretName: secretName,
								},
							},
						},
					},
				},
			},
		},
	}
}

func createRestartRule() *karov1alpha1.RestartRule {
	return &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restartRuleName,
			Namespace: testNamespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "ConfigMap",
					Name:       configMapName,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: deploymentName,
				},
			},
		},
	}
}

func createSecretRestartRule() *karov1alpha1.RestartRule {
	return &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-secret-restart-rule",
			Namespace: testNamespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "Secret",
					Name:       secretName,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: deploymentName,
				},
			},
		},
	}
}

func waitForDeploymentReady(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string) error {
	return wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		deployment, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		// Check if deployment is ready
		if deployment.Status.ReadyReplicas == *deployment.Spec.Replicas &&
			deployment.Status.Replicas == *deployment.Spec.Replicas &&
			deployment.Status.AvailableReplicas == *deployment.Spec.Replicas {
			return true, nil
		}

		return false, nil
	})
}

func cleanup(ctx context.Context, t *testing.T, clients *testClients) {
	t.Log("Cleaning up test resources...")

	// Stop controller manager first
	if err := clients.controllerManager.Stop(); err != nil {
		t.Logf("Error stopping controller manager: %v", err)
	}

	cleanupRestartRules(ctx, t, clients.k8sClient)
	cleanupK8sResources(ctx, t, clients.k8sClient)
	cleanupNamespace(ctx, t, clients.clientset)
}

func cleanupRestartRules(ctx context.Context, t *testing.T, k8sClient client.Client) {
	restartRules := []*karov1alpha1.RestartRule{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      restartRuleName,
				Namespace: testNamespace,
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "nginx-secret-restart-rule",
				Namespace: testNamespace,
			},
		},
	}

	for _, rule := range restartRules {
		deleteResource(ctx, t, k8sClient, rule, "RestartRule")
	}
}

func cleanupK8sResources(ctx context.Context, t *testing.T, k8sClient client.Client) {
	resources := []client.Object{
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deploymentName,
				Namespace: testNamespace,
			},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: testNamespace,
			},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: testNamespace,
			},
		},
	}

	for _, resource := range resources {
		resourceType := getResourceType(resource)
		deleteResource(ctx, t, k8sClient, resource, resourceType)
	}

	time.Sleep(2 * time.Second)
}

func cleanupNamespaceByName(ctx context.Context, t *testing.T, clientset *kubernetes.Clientset, namespace string) {
	if err := clientset.CoreV1().Namespaces().Delete(
		ctx, namespace, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
		t.Logf("Failed to delete namespace: %v", err)

		return
	}

	// Wait for namespace to be terminated
	t.Log("Waiting for namespace to be terminated...")
	timeout := 30 * time.Second
	startTime := time.Now()

	for {
		if time.Since(startTime) > timeout {
			t.Logf("Timeout waiting for namespace %s to be terminated", namespace)

			break
		}

		_, err := clientset.CoreV1().Namespaces().Get(ctx, namespace, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			t.Log("Namespace successfully terminated")

			break
		}

		if err != nil {
			t.Logf("Error checking namespace status: %v", err)

			break
		}

		time.Sleep(500 * time.Millisecond)
	}
}

func cleanupNamespace(ctx context.Context, t *testing.T, clientset *kubernetes.Clientset) {
	cleanupNamespaceByName(ctx, t, clientset, testNamespace)
}

func deleteResource(
	ctx context.Context,
	t *testing.T,
	k8sClient client.Client,
	resource client.Object,
	resourceType string,
) {
	if err := k8sClient.Delete(ctx, resource); err != nil && !apierrors.IsNotFound(err) {
		t.Logf("Failed to delete %s: %v", resourceType, err)
	}
}

func getResourceType(resource client.Object) string {
	switch resource.(type) {
	case *appsv1.Deployment:
		return "Deployment"
	case *corev1.ConfigMap:
		return "ConfigMap"
	case *corev1.Secret:
		return "Secret"
	default:
		return "Resource"
	}
}

// Helper functions for regex e2e tests

func createRegexTestConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "test",
			},
		},
		Data: map[string]string{
			"config": "initial-config-data",
		},
	}
}

func createRegexTestSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "test",
			},
		},
		Data: map[string][]byte{
			"api-key": []byte("initial-secret-data"),
		},
	}
}

func createRegexTestDeployment(namespace, name string) *appsv1.Deployment {
	replicas := int32(1)

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app": "test",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app":        "test",
					"deployment": name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":        "test",
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

func createRegexConfigMapRestartRule(namespace string) *karov1alpha1.RestartRule {
	return &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-configmap-regex-rule",
			Namespace: namespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "ConfigMap",
					Name:       ".*nginx.*-config",
					IsRegex:    true,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: "nginx-frontend",
				},
			},
		},
	}
}

func createRegexSecretRestartRule(namespace string) *karov1alpha1.RestartRule {
	return &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-secret-regex-rule",
			Namespace: namespace,
		},
		Spec: karov1alpha1.RestartRuleSpec{
			Changes: []karov1alpha1.ChangeSpec{
				{
					Kind:       "Secret",
					Name:       ".*nginx.*-secret",
					IsRegex:    true,
					ChangeType: []string{"Update"},
				},
			},
			Targets: []karov1alpha1.TargetSpec{
				{
					Kind: "Deployment",
					Name: "nginx-frontend",
				},
			},
		},
	}
}

// waitForRestartRuleReady waits for a RestartRule to be in Active phase with Ready condition true
func waitForRestartRuleReady(ctx context.Context, k8sClient client.Client, namespace, name string) error {
	return wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		rule := &karov1alpha1.RestartRule{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, rule); err != nil {
			return false, err
		}

		// Check if the rule is in Active phase
		if rule.Status.Phase != "Active" {
			return false, nil
		}

		// Check if Ready condition is true
		readyCondition := meta.FindStatusCondition(rule.Status.Conditions, "Ready")
		if readyCondition == nil || readyCondition.Status != metav1.ConditionTrue {
			return false, nil
		}

		return true, nil
	})
}

// waitForRestartEvent waits for a restart event to appear in the RestartRule status
func waitForRestartEvent(ctx context.Context, k8sClient client.Client, namespace, ruleName, targetName string) error {
	return wait.PollUntilContextTimeout(ctx, interval, timeout, true, func(ctx context.Context) (bool, error) {
		rule := &karov1alpha1.RestartRule{}
		if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ruleName}, rule); err != nil {
			return false, err
		}

		// Check if there's at least one restart event for the target
		for _, event := range rule.Status.RestartHistory {
			if event.Target.Name == targetName && event.Status == "Success" {
				return true, nil
			}
		}

		return false, nil
	})
}
