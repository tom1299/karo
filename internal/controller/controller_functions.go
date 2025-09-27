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

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
	"karo.jeeatwork.com/internal/store"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	// RestartAnnotation is the annotation key used to trigger deployment restarts
	RestartAnnotation = "karo.jeeatwork.com/restartedAt"

	// ConfigMapKind represents the ConfigMap resource kind
	ConfigMapKind = "ConfigMap"

	// DeploymentKind represents the Deployment resource kind
	DeploymentKind = "Deployment"
)

// BaseReconciler contains common fields and methods for resource controllers
type BaseReconciler struct {
	client.Client

	RestartRuleStore      store.RestartRuleStore
	DelayedRestartManager DelayedRestartManager
	operationType         store.OperationType
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

	restartAnnotation := RestartAnnotation
	deployment.Spec.Template.Annotations[restartAnnotation] = time.Now().Format(time.RFC3339)

	if err := r.Update(ctx, deployment); err != nil {
		return fmt.Errorf("failed to update deployment %s/%s: %w", targetNamespace, target.Name, err)
	}

	return nil
}

// ProcessRestartRules processes restart rules for deployments
func (r *BaseReconciler) ProcessRestartRules(ctx context.Context, restartRules []*karov1alpha1.RestartRule, resourceName, resourceType string) error {
	logger := log.FromContext(ctx)

	// Group restart rules by target
	targetRulesMap := r.groupRestartRulesByTarget(restartRules, resourceName, resourceType, logger)

	// Process each target group
	for targetKey, rules := range targetRulesMap {
		if err := r.processTargetGroup(ctx, targetKey, rules, resourceName, resourceType, logger); err != nil {
			return err
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

// groupRestartRulesByTarget groups restart rules by target key
func (r *BaseReconciler) groupRestartRulesByTarget(restartRules []*karov1alpha1.RestartRule, resourceName, resourceType string, logger logr.Logger) map[string][]*karov1alpha1.RestartRule {
	targetRulesMap := make(map[string][]*karov1alpha1.RestartRule)

	// For every restart rule returned by the store
	for _, rule := range restartRules {
		logger.Info("Processing restart rule",
			"restartRule", rule.Name,
			"namespace", rule.Namespace,
			"resource", resourceName,
			"resourceType", resourceType)

		// Get all targets and group by target key
		for _, target := range rule.Spec.Targets {
			if target.Kind == DeploymentKind {
				targetKey := r.getTargetKey(target, rule)
				targetRulesMap[targetKey] = append(targetRulesMap[targetKey], rule)
			}
		}
	}

	return targetRulesMap
}

// processTargetGroup processes a group of restart rules for a specific target
func (r *BaseReconciler) processTargetGroup(ctx context.Context, targetKey string, rules []*karov1alpha1.RestartRule, resourceName, resourceType string, logger logr.Logger) error {
	// Extract target information from the first rule (all should have same target)
	target := rules[0].Spec.Targets[0]

	// Create restart function that will be executed (immediately or after delay)
	restartFunc := r.createRestartFunction(ctx, target, rules, resourceName, resourceType, targetKey, logger)

	// Use DelayedRestartManager to handle the restart (with or without delay)
	if r.DelayedRestartManager != nil {
		r.DelayedRestartManager.ScheduleRestart(ctx, targetKey, rules, restartFunc)
	} else {
		// Fallback to immediate execution if manager is not available
		logger.Info("DelayedRestartManager not available, executing immediate restart")
		if err := restartFunc(); err != nil {
			logger.Error(err, "Failed to execute immediate restart")
		}
	}

	return nil
}

// createRestartFunction creates a restart function for the given target and rules
func (r *BaseReconciler) createRestartFunction(ctx context.Context, target karov1alpha1.TargetSpec, rules []*karov1alpha1.RestartRule, resourceName, resourceType, targetKey string, logger logr.Logger) func() error {
	rule := rules[0] // Use first rule for logging/status purposes

	return func() error {
		if err := r.RestartDeployment(ctx, target, rule); err != nil {
			r.handleRestartFailure(ctx, rules, target, resourceName, resourceType, targetKey, err, logger)

			return err
		}

		// Record successful restart in status for all rules
		for _, ruleItem := range rules {
			if statusErr := r.recordRestartEvent(ctx, ruleItem, target, resourceName, resourceType, "Success", ""); statusErr != nil {
				logger.Error(statusErr, "Failed to record restart event")
			}
		}

		// Log the successful restart
		logger.Info("Successfully restarted deployment",
			"deployment", target.Name,
			"resource", resourceName,
			"resourceType", resourceType,
			"targetKey", targetKey,
			"rulesCount", len(rules))

		return nil
	}
}

// handleRestartFailure handles the failure case when restarting a deployment
func (r *BaseReconciler) handleRestartFailure(ctx context.Context, rules []*karov1alpha1.RestartRule, target karov1alpha1.TargetSpec, resourceName, resourceType, targetKey string, err error, logger logr.Logger) {
	logger.Error(err, "Failed to restart deployment",
		"deployment", target.Name,
		"resource", resourceName,
		"resourceType", resourceType,
		"targetKey", targetKey)

	// Record failed restart in status for all rules
	for _, ruleItem := range rules {
		if statusErr := r.recordRestartEvent(ctx, ruleItem, target, resourceName, resourceType, "Failed", err.Error()); statusErr != nil {
			logger.Error(statusErr, "Failed to record restart event")
		}
	}
}

// getTargetKey generates a unique key for a target
func (r *BaseReconciler) getTargetKey(target karov1alpha1.TargetSpec, rule *karov1alpha1.RestartRule) string {
	targetNamespace := target.Namespace
	if targetNamespace == "" {
		targetNamespace = rule.Namespace
	}

	return fmt.Sprintf("%s/%s/%s", target.Kind, targetNamespace, target.Name)
}

// recordRestartEvent records a restart event in the RestartRule status
func (r *BaseReconciler) recordRestartEvent(ctx context.Context, rule *karov1alpha1.RestartRule, target karov1alpha1.TargetSpec, resourceName, resourceType, status, message string) error {
	// Fetch latest rule to avoid conflicts
	var currentRule karov1alpha1.RestartRule
	if err := r.Get(ctx, client.ObjectKeyFromObject(rule), &currentRule); err != nil {
		return fmt.Errorf("failed to get current RestartRule: %w", err)
	}

	targetNamespace := target.Namespace
	if targetNamespace == "" {
		targetNamespace = rule.Namespace
	}

	// Create restart event
	restartEvent := karov1alpha1.RestartEvent{
		Timestamp: metav1.Now(),
		Target: karov1alpha1.WorkloadReference{
			Kind:      target.Kind,
			Name:      target.Name,
			Namespace: targetNamespace,
		},
		TriggerResource: karov1alpha1.ResourceReference{
			Kind:      resourceType,
			Name:      resourceName,
			Namespace: rule.Namespace,
		},
		Status:  status,
		Message: message,
	}

	// Add to history (keep last 10 events)
	currentRule.Status.RestartHistory = append(currentRule.Status.RestartHistory, restartEvent)
	if len(currentRule.Status.RestartHistory) > 10 {
		currentRule.Status.RestartHistory = currentRule.Status.RestartHistory[len(currentRule.Status.RestartHistory)-10:]
	}

	// Update timestamp
	currentRule.Status.LastProcessedAt = &metav1.Time{Time: time.Now()}

	// Update status
	return r.Status().Update(ctx, &currentRule)
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
		Type:      ConfigMapKind,
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
