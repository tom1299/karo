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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"karo.jeeatwork.com/internal/store"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type ConfigMapReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	RestartRuleStore store.RestartRuleStore
	operationType    store.OperationType
}

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var configMap corev1.ConfigMap
	if err := r.Get(ctx, req.NamespacedName, &configMap); err != nil {
		if r.operationType != store.OperationDelete {
			logger.Error(err, "Unable to fetch ConfigMap")
			return ctrl.Result{}, fmt.Errorf("unable to fetch ConfigMap: %w", err)
		}
	}

	logger.Info("ConfigMap event received",
		"operation", r.operationType,
		"name", configMap.Name,
		"namespace", configMap.Namespace)

	// Check if this is an update event
	if r.operationType == store.OperationUpdate {
		// Get matching restart rules from the store
		restartRules := r.RestartRuleStore.GetForConfigMap(ctx, configMap, store.OperationUpdate)

		// For every restart rule returned by the store
		for _, rule := range restartRules {
			logger.Info("Processing restart rule",
				"restartRule", rule.Name,
				"namespace", rule.Namespace,
				"configMap", configMap.Name)

			// Get all targets and iterate over them
			for _, target := range rule.Spec.Targets {
				// If the target is a Deployment, do a rollout restart
				if target.Kind == "Deployment" {
					if err := r.restartDeployment(ctx, target, rule); err != nil {
						logger.Error(err, "Failed to restart deployment",
							"deployment", target.Name,
							"configMap", configMap.Name,
							"restartRule", rule.Name)
						continue
					}

					// Log the restart of the deployment
					logger.Info("Successfully restarted deployment",
						"deployment", target.Name,
						"configMap", configMap.Name,
						"restartRule", rule.Name,
						"namespace", target.Namespace)
				}
			}
		}
	}

	return ctrl.Result{}, nil
}

// restartDeployment performs a rollout restart of the target deployment
func (r *ConfigMapReconciler) restartDeployment(ctx context.Context, target karov1alpha1.TargetSpec, rule *karov1alpha1.RestartRule) error {

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

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigMapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("configmap").
		WithEventFilter(
			predicate.Funcs{
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
			}).
		For(&corev1.ConfigMap{}).
		Complete(r)
}
