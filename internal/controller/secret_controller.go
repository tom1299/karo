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

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"karo.jeeatwork.com/internal/store"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type SecretReconciler struct {
	BaseReconciler

	Scheme *runtime.Scheme
}

func (r *SecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var secret corev1.Secret
	if err := r.Get(ctx, req.NamespacedName, &secret); err != nil {
		if r.operationType != store.OperationDelete {
			logger.Error(err, "Unable to fetch Secret")

			return ctrl.Result{}, fmt.Errorf("unable to fetch Secret: %w", err)
		}
	}

	resourceInfo := GetSecretInfo(secret)
	logger.Info("Secret event received",
		"operation", r.operationType,
		"name", resourceInfo.Name,
		"namespace", resourceInfo.Namespace)

	// Process any delayed restarts that are ready to execute
	r.ProcessDelayedRestarts(ctx)

	// Check if this is an update event
	if r.operationType == store.OperationUpdate {
		// Get matching restart rules from the store
		restartRules := r.RestartRuleStore.GetForSecret(ctx, secret, store.OperationUpdate)

		// Process restart rules using common function
		if err := r.ProcessRestartRules(ctx, restartRules, resourceInfo.Name, resourceInfo.Type); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("secret").
		WithEventFilter(r.CreateEventFilter()).
		For(&corev1.Secret{}).
		Complete(r)
}
