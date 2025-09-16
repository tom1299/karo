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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"karo.jeeatwork.com/internal/store"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	log "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

type ConfigMapReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	RestartRuleStore *store.RestartRuleStore
	operationType    OperationType
}

type OperationType string

const (
	OperationCreate OperationType = "Create"
	OperationUpdate OperationType = "Update"
	OperationDelete OperationType = "Delete"
)

func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	logger := log.FromContext(ctx)

	var configMap corev1.ConfigMap
	if err := r.Get(ctx, req.NamespacedName, &configMap); err != nil {
		// If the ConfigMap doesn't exist and we're handling a delete operation, log it
		if r.operationType == OperationDelete {
			logger.Info("ConfigMap event received",
				"operation", r.operationType,
				"name", req.Name,
				"namespace", req.Namespace)
		} else {
			// Otherwise, it's an error we should log
			logger.Error(err, "Unable to fetch ConfigMap")
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("ConfigMap event received",
		"operation", r.operationType,
		"name", configMap.Name,
		"namespace", configMap.Namespace)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ConfigMapReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a new controller builder
	return ctrl.NewControllerManagedBy(mgr).
		Named("configmap").
		WithEventFilter(
			predicate.Funcs{
				CreateFunc: func(e event.CreateEvent) bool {
					// Set operation type to Create
					r.operationType = OperationCreate
					return true
				},
				UpdateFunc: func(e event.UpdateEvent) bool {
					// Set operation type to Update
					r.operationType = OperationUpdate
					return true
				},
				DeleteFunc: func(e event.DeleteEvent) bool {
					// Set operation type to Delete
					r.operationType = OperationDelete
					return true
				},
			}).
		For(&corev1.ConfigMap{}).
		Complete(r)
}
