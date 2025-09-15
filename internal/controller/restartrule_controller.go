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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

// RestartRuleReconciler reconciles a RestartRule object
type RestartRuleReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	RestartRules map[string]karov1alpha1.RestartRuleSpec
}

// +kubebuilder:rbac:groups=karo.jeeatwork.com,resources=restartrules,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=karo.jeeatwork.com,resources=restartrules/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=karo.jeeatwork.com,resources=restartrules/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the RestartRule object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *RestartRuleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the RestartRule instance
	restartRule := &karov1alpha1.RestartRule{}
	err := r.Get(ctx, req.NamespacedName, restartRule)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			log.Error(err, "Unable to fetch RestartRule")
			return ctrl.Result{}, err
		}

		// RestartRule was deleted
		// Create a key for the rule using its namespace and name
		ruleKey := req.Namespace + "/" + req.Name

		// Check if it exists in our in-memory map
		if _, exists := r.RestartRules[ruleKey]; exists {
			log.Info("Deleting RestartRule from context", "key", ruleKey)
			// Delete the rule from our map
			delete(r.RestartRules, ruleKey)
		}

		return ctrl.Result{}, nil
	}

	// RestartRule exists, extract the ConfigMap name it refers to
	configMapName := restartRule.Spec.When.ConfigMapChange
	if configMapName == "" {
		log.Info("RestartRule does not reference a ConfigMap, ignoring", "name", restartRule.Name)
		return ctrl.Result{}, nil
	}

	// Create a key for the rule using its namespace and name
	ruleKey := restartRule.Namespace + "/" + restartRule.Name

	if _, exists := r.RestartRules[ruleKey]; exists {
		log.Info("RestartRule already exists in context")
		r.RestartRules[ruleKey] = restartRule.Spec
	} else {
		// Rule doesn't exist, add it
		log.Info("Adding new RestartRule to context",
			"key", ruleKey,
			"configMap", configMapName,
			"restart", restartRule.Spec.Then.Restart)
		r.RestartRules[ruleKey] = restartRule.Spec
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RestartRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.RestartRule{}).
		Named("restartrule").
		Complete(r)
}
