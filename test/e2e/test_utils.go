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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	configMapName   = "nginx-config"
	deploymentName  = "nginx"
	interval        = 10 * time.Second
	restartRuleName = "nginx-restart-rule"
	testNamespace   = "karo-e2e-test"
	timeout         = 5 * time.Minute
)

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
	if err != nil && !errors.IsAlreadyExists(err) {
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
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "nginx-config",
									MountPath: "/etc/nginx/nginx.conf",
									SubPath:   "nginx.conf",
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

func cleanup(ctx context.Context, t *testing.T, clientset *kubernetes.Clientset, k8sClient client.Client) {
	t.Log("Cleaning up test resources...")

	// Delete RestartRule
	restartRule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{
			Name:      restartRuleName,
			Namespace: testNamespace,
		},
	}
	if err := k8sClient.Delete(ctx, restartRule); err != nil && !errors.IsNotFound(err) {
		t.Logf("Failed to delete RestartRule: %v", err)
	}

	// Delete Deployment
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: testNamespace,
		},
	}
	if err := k8sClient.Delete(ctx, deployment); err != nil && !errors.IsNotFound(err) {
		t.Logf("Failed to delete Deployment: %v", err)
	}

	// Delete ConfigMap
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: testNamespace,
		},
	}
	if err := k8sClient.Delete(ctx, configMap); err != nil && !errors.IsNotFound(err) {
		t.Logf("Failed to delete ConfigMap: %v", err)
	}

	// Wait a bit for resources to be deleted
	time.Sleep(2 * time.Second)

	// Delete namespace
	if err := clientset.CoreV1().Namespaces().Delete(
		ctx, testNamespace, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
		t.Logf("Failed to delete namespace: %v", err)
	}
}
