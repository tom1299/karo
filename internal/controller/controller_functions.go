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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

// targetInfo holds a target and the rules that apply to it.
type targetInfo struct {
	target karov1alpha1.TargetSpec
	rules  []*karov1alpha1.RestartRule
}

// collectUniqueTargets gathers all unique deployment targets from a list of restart rules.
func collectUniqueTargets(restartRules []*karov1alpha1.RestartRule) map[string]targetInfo {
	uniqueTargets := make(map[string]targetInfo)
	for _, rule := range restartRules {
		for _, target := range rule.Spec.Targets {
			if target.Kind != "Deployment" {
				continue
			}

			targetNamespace := target.Namespace
			if targetNamespace == "" {
				targetNamespace = rule.Namespace
			}
			targetKey := fmt.Sprintf("%s/%s/%s", target.Kind, targetNamespace, target.Name)

			data := uniqueTargets[targetKey]
			if data.rules == nil {
				data.target = target
			}
			data.rules = append(data.rules, rule)
			uniqueTargets[targetKey] = data
		}
	}

	return uniqueTargets
}

// logRestartDetails logs information about the restart being processed.
func logRestartDetails(ctx context.Context, data targetInfo, resourceName, resourceType string) {
	logger := log.FromContext(ctx)
	target := data.target
	rules := data.rules
	ruleForContext := rules[0]

	targetNamespace := target.Namespace
	if targetNamespace == "" {
		targetNamespace = ruleForContext.Namespace
	}

	logger.Info("Processing restart rule(s) for target",
		"target", target.Name,
		"namespace", targetNamespace,
		"resource", resourceName,
		"resourceType", resourceType)

	if len(rules) > 1 {
		ruleNames := make([]string, len(rules))
		for i, rule := range rules {
			ruleNames[i] = rule.Name
		}
		logger.Info("Multiple restart rules match the same target; a single restart will be performed",
			"target", target.Name,
			"namespace", targetNamespace,
			"rules", ruleNames)
	}
}

// ProcessRestartRules processes restart rules for deployments.
func (r *BaseReconciler) ProcessRestartRules(ctx context.Context, restartRules []*karov1alpha1.RestartRule, resourceName, resourceType string) error {
	uniqueTargets := collectUniqueTargets(restartRules)

	for _, data := range uniqueTargets {
		logRestartDetails(ctx, data, resourceName, resourceType)
		r.performRestartAndRecordEvents(ctx, data, resourceName, resourceType)
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

// performRestartAndRecordEvents handles the deployment restart and updates the status of all associated rules.
func (r *BaseReconciler) performRestartAndRecordEvents(ctx context.Context, data targetInfo, resourceName, resourceType string) {
	logger := log.FromContext(ctx)
	target := data.target
	rules := data.rules
	ruleForRestartContext := rules[0]

	err := r.RestartDeployment(ctx, target, ruleForRestartContext)

	status := "Success"
	message := ""
	if err != nil {
		status = "Failed"
		message = err.Error()
		logger.Error(err, "Failed to restart deployment", "deployment", target.Name)
	} else {
		logger.Info("Successfully restarted deployment", "deployment", target.Name)
	}

	// Record the outcome for all associated rules
	for _, rule := range rules {
		if statusErr := r.recordRestartEvent(ctx, rule, target, resourceName, resourceType, status, message); statusErr != nil {
			logger.Error(statusErr, "Failed to record restart event for rule", "rule", rule.Name)
		}
	}
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
