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
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// ConfigMapReconciler reconciles a ConfigMap object
type ConfigMapReconciler struct {
	client.Client
	Scheme           *runtime.Scheme
	RestartRuleStore *store.RestartRuleStore
	operationType    OperationType
}

// +kubebuilder:rbac:groups=core,resources=configmaps/status,verbs=get;update;patch

type OperationType string

const (
	OperationCreate OperationType = "Create"
	OperationUpdate OperationType = "Update"
	OperationDelete OperationType = "Delete"
)

// +kubebuilder:rbac:groups=core,resources=configmaps/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ConfigMap object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *ConfigMapReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// log := logf.FromContext(ctx)
	//
	//// Fetch the ConfigMap instance
	//configMap := &corev1.ConfigMap{}
	//if err := r.Get(ctx, req.NamespacedName, configMap); err != nil {
	//	if client.IgnoreNotFound(err) != nil {
	//		log.Error(err, "Unable to fetch ConfigMap")
	//		return ctrl.Result{}, err
	//	}
	//	// ConfigMap not found, likely deleted, return without error
	//	return ctrl.Result{}, nil
	//}
	//
	//log.V(1).Info("Reconciling ConfigMap", "name", configMap.Name, "namespace", configMap.Namespace)

	// ruleKey := configMap.Namespace + "/" + configMap.Name

	//restartRule, exists := r.RestartRules[ruleKey]
	//if !exists {
	//	log.V(1).Info("No RestartRule found for ConfigMap", "key", ruleKey)
	//	return ctrl.Result{}, nil
	//}

	//var kind, depName string
	//fmtParts := []rune(restartRule.Then.Restart)
	//for i, c := range fmtParts {
	//	if c == '/' {
	//		kind = string(fmtParts[:i])
	//		depName = string(fmtParts[i+1:])
	//		break
	//	}
	//}
	//if kind == "Deployment" && depName != "" {
	//	dep := &appsv1.Deployment{}
	//	depKey := client.ObjectKey{Namespace: configMap.Namespace, Name: depName}
	//	if err := r.Get(ctx, depKey, dep); err != nil {
	//		log.Error(err, "Unable to fetch Deployment for rollout", "deployment", depName)
	//	}
	//	patch := []byte(`{
	//		"spec": {
	//			"template": {
	//				"metadata": {
	//					"annotations": {
	//						"karo.jeeatwork.com/restartedAt": "` + time.Now().Format(time.RFC3339) + `"
	//					}
	//				}
	//			}
	//		}
	//	}`)
	//	if err := r.Patch(ctx, dep, client.RawPatch(types.StrategicMergePatchType, patch)); err != nil {
	//		log.Error(err, "Failed to patch Deployment for rollout", "deployment", depName)
	//	} else {
	//		log.Info("Patched deployment for rollout due to ConfigMap change", "deployment", depName, "configmap", configMap.Name)
	//	}
	//}

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
