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

// ProcessRestartRules processes restart rules for deployments with delay support
func (r *BaseReconciler) ProcessRestartRules(ctx context.Context, restartRules []*karov1alpha1.RestartRule, resourceName, resourceType string) error {
	logger := log.FromContext(ctx)

	// Group targets by workload key to handle multiple rules applying to the same target
	type targetInfo struct {
		target   karov1alpha1.TargetSpec
		rules    []*karov1alpha1.RestartRule
		maxDelay *time.Duration
	}
	targetMap := make(map[string]*targetInfo)

	// First, collect all targets and find the maximum delay for each
	for _, rule := range restartRules {
		logger.Info("Processing restart rule",
			"restartRule", rule.Name,
			"namespace", rule.Namespace,
			"resource", resourceName,
			"resourceType", resourceType)

		for _, target := range rule.Spec.Targets {
			if target.Kind != "Deployment" {
				continue // Only handle Deployment targets for now
			}

			targetNamespace := target.Namespace
			if targetNamespace == "" {
				targetNamespace = rule.Namespace
			}
			workloadKey := fmt.Sprintf("%s/%s", targetNamespace, target.Name)

			// Get or create target info
			info, exists := targetMap[workloadKey]
			if !exists {
				info = &targetInfo{
					target: target,
					rules:  make([]*karov1alpha1.RestartRule, 0),
				}
				targetMap[workloadKey] = info
			}

			// Add this rule
			info.rules = append(info.rules, rule)

			// Update maximum delay
			if rule.Spec.DelayRestart != nil {
				delay := rule.Spec.DelayRestart.Duration
				if info.maxDelay == nil || delay > *info.maxDelay {
					info.maxDelay = &delay
				}
			}
		}
	}

	// Process each unique target
	for workloadKey, info := range targetMap {
		target := info.target
		targetNamespace := target.Namespace
		if targetNamespace == "" {
			targetNamespace = info.rules[0].Namespace
		}

		// Check if workload is already delayed
		if r.RestartRuleStore.IsWorkloadDelayed(ctx, workloadKey, target.Kind) {
			logger.Info("Skipping restart of already delayed workload",
				"deployment", target.Name,
				"namespace", targetNamespace,
				"resource", resourceName,
				"resourceType", resourceType,
				"workloadKey", workloadKey)

			continue
		}

		// If there's a delay, schedule delayed restart
		if info.maxDelay != nil {
			r.RestartRuleStore.AddDelayedRestart(ctx, workloadKey, target.Kind, *info.maxDelay)

			logger.Info("✅ DELAY: Scheduled delayed restart for deployment",
				"deployment", target.Name,
				"namespace", targetNamespace,
				"delay", info.maxDelay.String(),
				"resource", resourceName,
				"resourceType", resourceType,
				"workloadKey", workloadKey)

			// Record delayed restart events for all rules
			for _, rule := range info.rules {
				message := "Restart delayed by " + info.maxDelay.String()
				if statusErr := r.recordRestartEvent(ctx, rule, target, resourceName, resourceType, "Delayed", message); statusErr != nil {
					logger.Error(statusErr, "Failed to record delayed restart event")
				}
			}

			continue
		}

		// No delay, restart immediately
		if err := r.RestartDeployment(ctx, target, info.rules[0]); err != nil {
			logger.Error(err, "Failed to restart deployment",
				"deployment", target.Name,
				"resource", resourceName,
				"resourceType", resourceType,
				"workloadKey", workloadKey)

			// Record failed restart in status for all rules
			for _, rule := range info.rules {
				if statusErr := r.recordRestartEvent(ctx, rule, target, resourceName, resourceType, "Failed", err.Error()); statusErr != nil {
					logger.Error(statusErr, "Failed to record restart event")
				}
			}

			continue
		}

		// Record successful restart in status for all rules
		for _, rule := range info.rules {
			if statusErr := r.recordRestartEvent(ctx, rule, target, resourceName, resourceType, "Success", ""); statusErr != nil {
				logger.Error(statusErr, "Failed to record restart event")
			}
		}

		logger.Info("Successfully restarted deployment",
			"deployment", target.Name,
			"namespace", targetNamespace,
			"resource", resourceName,
			"resourceType", resourceType,
			"workloadKey", workloadKey)
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

// ProcessDelayedRestarts processes all delayed restarts that are ready to execute
func (r *BaseReconciler) ProcessDelayedRestarts(ctx context.Context) {
	logger := log.FromContext(ctx)

	delayedRestarts := r.RestartRuleStore.GetDelayedRestarts(ctx)
	now := time.Now()

	if len(delayedRestarts) > 0 {
		logger.Info("🔄 DELAY: Processing delayed restarts",
			"count", len(delayedRestarts),
			"currentTime", now.Format(time.RFC3339))
	}

	for _, delayedRestart := range delayedRestarts {
		// Check if the delay has expired
		if now.Before(delayedRestart.RestartAt) {
			continue // Not yet time to restart
		}

		logger.Info("⚡ DELAY: Executing delayed restart NOW",
			"workloadKey", delayedRestart.WorkloadKey,
			"workloadKind", delayedRestart.WorkloadKind,
			"delay", delayedRestart.Delay.String(),
			"scheduledAt", delayedRestart.ScheduledAt.Format(time.RFC3339),
			"restartAt", delayedRestart.RestartAt.Format(time.RFC3339))

		// Remove the delayed restart from tracking
		r.RestartRuleStore.RemoveDelayedRestart(ctx, delayedRestart.WorkloadKey, delayedRestart.WorkloadKind)

		// Parse workload key to get namespace and name
		parts := splitWorkloadKey(delayedRestart.WorkloadKey)
		if len(parts) != 2 {
			logger.Error(nil, "Invalid workload key format",
				"workloadKey", delayedRestart.WorkloadKey,
				"expected", "namespace/name")

			continue
		}

		namespace := parts[0]
		name := parts[1]

		// Create a target spec for the restart
		target := karov1alpha1.TargetSpec{
			Kind:      delayedRestart.WorkloadKind,
			Name:      name,
			Namespace: namespace,
		}

		// Create a minimal rule for context (we need this for recordRestartEvent)
		rule := &karov1alpha1.RestartRule{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "delayed-restart",
				Namespace: namespace,
			},
		}

		// Execute the restart
		if delayedRestart.WorkloadKind == "Deployment" {
			if err := r.RestartDeployment(ctx, target, rule); err != nil {
				logger.Error(err, "Failed to execute delayed restart",
					"deployment", name,
					"namespace", namespace,
					"workloadKey", delayedRestart.WorkloadKey)

				// Record failed restart in status
				if statusErr := r.recordRestartEvent(ctx, rule, target, "delayed", "restart", "Failed", err.Error()); statusErr != nil {
					logger.Error(statusErr, "Failed to record delayed restart event")
				}

				continue
			}

			// Record successful delayed restart in status
			message := "Delayed restart executed after " + delayedRestart.Delay.String()
			if statusErr := r.recordRestartEvent(ctx, rule, target, "delayed", "restart", "Success", message); statusErr != nil {
				logger.Error(statusErr, "Failed to record delayed restart event")
			}

			logger.Info("Successfully executed delayed restart",
				"deployment", name,
				"namespace", namespace,
				"delay", delayedRestart.Delay.String())
		}
	}
}

// splitWorkloadKey splits a workload key in the format "namespace/name"
func splitWorkloadKey(workloadKey string) []string {
	// Split by "/" to get namespace and name
	parts := make([]string, 0, 2)
	lastSlash := -1
	for i := len(workloadKey) - 1; i >= 0; i-- {
		if workloadKey[i] == '/' {
			lastSlash = i

			break
		}
	}

	if lastSlash == -1 {
		return []string{workloadKey} // No slash found
	}

	parts = append(parts, workloadKey[:lastSlash])   // namespace
	parts = append(parts, workloadKey[lastSlash+1:]) // name

	return parts
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
