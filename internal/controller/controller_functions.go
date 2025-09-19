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

package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"karo.jeeatwork.com/internal/store"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// BaseReconciler contains common fields and methods for resource controllers
type BaseReconciler struct {
	client.Client
	RestartRuleStore store.RestartRuleStore
	operationType    store.OperationType
}

// RestartDeployment performs a rollout restart of the target deployment
func (r *BaseReconciler) RestartDeployment(ctx context.Context, target karov1alpha1.TargetSpec, rule *karov1alpha1.RestartRule) error {
	targetNamespace := target.Namespace
	if targetNamespace == "" {
		targetNamespace = rule.Namespace
	}

	// Get the deployment
	deployment := &appsv1.Deployment{}
	deploymentKey := types.NamespacedName{
		Name:      target.Name,
		Namespace: targetNamespace,
	}

	if err := r.Get(ctx, deploymentKey, deployment); err != nil {
		return fmt.Errorf("failed to get deployment %s/%s: %w", targetNamespace, target.Name, err)
	}

	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = make(map[string]string)
	}

	restartAnnotation := "karo.jeeatwork.com/restartedAt"
	deployment.Spec.Template.Annotations[restartAnnotation] = time.Now().Format(time.RFC3339)

	if err := r.Update(ctx, deployment); err != nil {
		return fmt.Errorf("failed to update deployment %s/%s: %w", targetNamespace, target.Name, err)
	}

	return nil
}

// ProcessRestartRules processes restart rules for deployments
func (r *BaseReconciler) ProcessRestartRules(ctx context.Context, restartRules []*karov1alpha1.RestartRule, resourceName, resourceType string) error {
	logger := log.FromContext(ctx)

	// For every restart rule returned by the store
	for _, rule := range restartRules {
		logger.Info("Processing restart rule",
			"restartRule", rule.Name,
			"namespace", rule.Namespace,
			"resource", resourceName,
			"resourceType", resourceType)

		// Get all targets and iterate over them
		for _, target := range rule.Spec.Targets {
			// If the target is a Deployment, do a rollout restart
			if target.Kind == "Deployment" {
				if err := r.RestartDeployment(ctx, target, rule); err != nil {
					logger.Error(err, "Failed to restart deployment",
						"deployment", target.Name,
						"resource", resourceName,
						"resourceType", resourceType,
						"restartRule", rule.Name)
					continue
				}

				// Log the restart of the deployment
				logger.Info("Successfully restarted deployment",
					"deployment", target.Name,
					"resource", resourceName,
					"resourceType", resourceType,
					"restartRule", rule.Name,
					"namespace", target.Namespace)
			}
		}
	}

	return nil
}

// CreateEventFilter creates the common event filter predicate
func (r *BaseReconciler) CreateEventFilter() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			r.operationType = store.OperationCreate
			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			r.operationType = store.OperationUpdate
			return true
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			r.operationType = store.OperationDelete
			return true
		},
	}
}

// ResourceInfo contains information about a Kubernetes resource
type ResourceInfo struct {
	Name      string
	Namespace string
	Type      string
}

// GetConfigMapInfo extracts resource information from a ConfigMap
func GetConfigMapInfo(configMap corev1.ConfigMap) ResourceInfo {
	return ResourceInfo{
		Name:      configMap.Name,
		Namespace: configMap.Namespace,
		Type:      "ConfigMap",
	}
}

// GetSecretInfo extracts resource information from a Secret
func GetSecretInfo(secret corev1.Secret) ResourceInfo {
	return ResourceInfo{
		Name:      secret.Name,
		Namespace: secret.Namespace,
		Type:      "Secret",
	}
}
