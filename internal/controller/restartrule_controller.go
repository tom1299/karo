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

	"k8s.io/apimachinery/pkg/runtime"
	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"karo.jeeatwork.com/internal/store"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// RestartRuleReconciler reconciles a RestartRule object
type RestartRuleReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	RestartRuleStore store.RestartRuleStore
}

func (r *RestartRuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.V(1).Info("Reconciling RestartRule", "name", req.Name, "namespace", req.Namespace)

	var rule karov1alpha1.RestartRule
	err := r.Get(ctx, req.NamespacedName, &rule)
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Object deleted, remove from store
			r.RestartRuleStore.Remove(ctx, req.Namespace, req.Name)
			log.V(1).Info("RestartRule deleted from store", "name", req.Name, "namespace", req.Namespace)
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get RestartRule")
		return ctrl.Result{}, err
	}

	// Object exists, add/update in store
	r.RestartRuleStore.Add(ctx, &rule)
	log.V(1).Info("RestartRule added/updated in store", "name", req.Name, "namespace", req.Namespace)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RestartRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.RestartRule{}).
		Named("restartrule").
		Complete(r)
}
