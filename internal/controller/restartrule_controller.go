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
	"regexp"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	// Set initial phase if not set
	if rule.Status.Phase == "" {
		rule.Status.Phase = "Pending"
	}

	// Validate rule configuration
	if err := r.validateRule(&rule); err != nil {
		return r.updateStatusWithError(ctx, &rule, err)
	}

	// Add to store (separate from validation)
	r.RestartRuleStore.Add(ctx, &rule)
	log.V(1).Info("RestartRule added/updated in store", "name", req.Name, "namespace", req.Namespace)

	// Update status to Active
	return r.updateStatusActive(ctx, &rule)
}

// validateRule validates the RestartRule configuration, especially regex patterns
func (r *RestartRuleReconciler) validateRule(rule *karov1alpha1.RestartRule) error {
	// Validate change specs
	for i, change := range rule.Spec.Changes {
		// Validate that either name or selector is specified (but not both)
		if change.Name != "" && change.Selector != nil {
			return fmt.Errorf("change[%d]: cannot specify both name and selector", i)
		}

		// Validate regex patterns if IsRegex is true
		if change.IsRegex && change.Name != "" {
			if _, err := regexp.Compile(change.Name); err != nil {
				return fmt.Errorf("change[%d]: invalid regex pattern in name %q: %w", i, change.Name, err)
			}
		}
	}

	// Validate target specs
	for i, target := range rule.Spec.Targets {
		// Validate that either name or selector is specified (but not both)
		if target.Name != "" && target.Selector != nil {
			return fmt.Errorf("target[%d]: cannot specify both name and selector", i)
		}
	}

	return nil
}

// updateStatusActive updates the status to Active phase
func (r *RestartRuleReconciler) updateStatusActive(ctx context.Context, rule *karov1alpha1.RestartRule) (ctrl.Result, error) {
	rule.Status.Phase = "Active"
	rule.Status.LastProcessedAt = &metav1.Time{Time: time.Now()}

	// Update Ready condition
	meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "RuleActive",
		Message: "RestartRule is active and monitoring for changes",
	})

	// Update Valid condition
	meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{
		Type:    "Valid",
		Status:  metav1.ConditionTrue,
		Reason:  "ValidationPassed",
		Message: "RestartRule configuration is valid",
	})

	return ctrl.Result{}, r.Status().Update(ctx, rule)
}

// updateStatusWithError updates the status when validation fails
func (r *RestartRuleReconciler) updateStatusWithError(ctx context.Context, rule *karov1alpha1.RestartRule, validationErr error) (ctrl.Result, error) {
	rule.Status.Phase = "Invalid"
	rule.Status.LastProcessedAt = &metav1.Time{Time: time.Now()}

	// Update Ready condition
	meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionFalse,
		Reason:  "ValidationFailed",
		Message: "RestartRule validation failed",
	})

	// Update Valid condition
	meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{
		Type:    "Valid",
		Status:  metav1.ConditionFalse,
		Reason:  "ValidationFailed",
		Message: validationErr.Error(),
	})

	if err := r.Status().Update(ctx, rule); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	return ctrl.Result{}, validationErr
}

// SetupWithManager sets up the controller with the Manager.
func (r *RestartRuleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&karov1alpha1.RestartRule{}).
		Named("restartrule").
		Complete(r)
}
