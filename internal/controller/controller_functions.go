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
	"sync"
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

// DelayedRestart represents a target that is scheduled for delayed restart
type DelayedRestart struct {
	Target        karov1alpha1.TargetSpec
	Rule          *karov1alpha1.RestartRule
	ScheduledTime time.Time
	MaxDelay      time.Duration
	TriggerInfo   ResourceInfo
}

// DelayManager manages delayed restarts for targets
type DelayManager struct {
	mu       sync.RWMutex
	restarts map[string]*DelayedRestart // key: namespace/kind/name
}

// Global delay manager instance
var globalDelayManager = &DelayManager{
	restarts: make(map[string]*DelayedRestart),
}

// GetDelayManager returns the global delay manager instance
func GetDelayManager() *DelayManager {
	return globalDelayManager
}

// getTargetKey generates a unique key for a target
func (dm *DelayManager) getTargetKey(target karov1alpha1.TargetSpec, rule *karov1alpha1.RestartRule) string {
	targetNamespace := target.Namespace
	if targetNamespace == "" {
		targetNamespace = rule.Namespace
	}
	return fmt.Sprintf("%s/%s/%s", targetNamespace, target.Kind, target.Name)
}

// ScheduleRestart schedules a target for delayed restart or updates existing delay with max delay
func (dm *DelayManager) ScheduleRestart(ctx context.Context, target karov1alpha1.TargetSpec, rule *karov1alpha1.RestartRule, triggerInfo ResourceInfo, delay time.Duration) bool {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	key := dm.getTargetKey(target, rule)
	logger := log.FromContext(ctx)

	existing, exists := dm.restarts[key]
	if exists {
		// Target is already scheduled for restart
		logger.Info("Target already scheduled for delayed restart - only logging info",
			"target", key,
			"existingDelay", existing.MaxDelay,
			"newDelay", delay,
			"scheduledTime", existing.ScheduledTime.Format(time.RFC3339))
		return false // Don't schedule new restart
	}

	// Schedule new delayed restart
	scheduledTime := time.Now().Add(delay)
	dm.restarts[key] = &DelayedRestart{
		Target:        target,
		Rule:          rule,
		ScheduledTime: scheduledTime,
		MaxDelay:      delay,
		TriggerInfo:   triggerInfo,
	}

	logger.Info("Scheduled delayed restart",
		"target", key,
		"delay", delay,
		"scheduledTime", scheduledTime.Format(time.RFC3339))

	return true // New restart scheduled
}

// IsTargetScheduled checks if a target is already scheduled for delayed restart
func (dm *DelayManager) IsTargetScheduled(target karov1alpha1.TargetSpec, rule *karov1alpha1.RestartRule) bool {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	key := dm.getTargetKey(target, rule)
	_, exists := dm.restarts[key]
	return exists
}

// GetDueRestarts returns and removes restarts that are due for execution
func (dm *DelayManager) GetDueRestarts() []*DelayedRestart {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	var due []*DelayedRestart
	now := time.Now()

	for key, restart := range dm.restarts {
		if restart.ScheduledTime.Before(now) || restart.ScheduledTime.Equal(now) {
			due = append(due, restart)
			delete(dm.restarts, key)
		}
	}

	return due
}

// RemoveTarget removes a target from the delayed restart queue
func (dm *DelayManager) RemoveTarget(target karov1alpha1.TargetSpec, rule *karov1alpha1.RestartRule) {
	dm.mu.Lock()
	defer dm.mu.Unlock()

	key := dm.getTargetKey(target, rule)
	delete(dm.restarts, key)
}

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

// ProcessRestartRules processes restart rules for deployments
func (r *BaseReconciler) ProcessRestartRules(ctx context.Context, restartRules []*karov1alpha1.RestartRule, resourceName, resourceType string) error {
	logger := log.FromContext(ctx)

	triggerInfo := ResourceInfo{
		Name:      resourceName,
		Namespace: restartRules[0].Namespace, // All rules should be from same namespace
		Type:      resourceType,
	}

	// Group targets by unique key and calculate delays
	targetDelays := make(map[string]struct {
		target   karov1alpha1.TargetSpec
		rule     *karov1alpha1.RestartRule
		maxDelay time.Duration
		hasDelay bool
	})

	// First pass: collect all targets and calculate max delays
	for _, rule := range restartRules {
		logger.Info("Processing restart rule",
			"restartRule", rule.Name,
			"namespace", rule.Namespace,
			"resource", resourceName,
			"resourceType", resourceType)

		for _, target := range rule.Spec.Targets {
			if target.Kind == "Deployment" {
				delayManager := GetDelayManager()
				key := delayManager.getTargetKey(target, rule)

				current := targetDelays[key]
				current.target = target
				// Keep the first rule for reference - this is just for logging/status
				if current.rule == nil {
					current.rule = rule
				}

				if rule.Spec.DelayRestart != nil {
					current.hasDelay = true
					delay := rule.Spec.DelayRestart.Duration
					if delay > current.maxDelay {
						current.maxDelay = delay
					}
					logger.Info("Found delay for target",
						"target", target.Name,
						"delay", delay,
						"currentMaxDelay", current.maxDelay,
						"restartRule", rule.Name)
				}

				targetDelays[key] = current
			}
		}
	}

	// Second pass: execute restarts based on delay logic
	for key, targetInfo := range targetDelays {
		if targetInfo.hasDelay {
			delayManager := GetDelayManager()

			// Try to schedule delayed restart
			if delayManager.ScheduleRestart(ctx, targetInfo.target, targetInfo.rule, triggerInfo, targetInfo.maxDelay) {
				logger.Info("Target scheduled for delayed restart",
					"target", targetInfo.target.Name,
					"delay", targetInfo.maxDelay,
					"targetKey", key)

				// Start goroutine to handle the delayed restart with background context
				go r.handleDelayedRestart(context.Background(), targetInfo.target, targetInfo.rule, triggerInfo, targetInfo.maxDelay)
			} else {
				logger.Info("Target already scheduled for restart, no action taken",
					"target", targetInfo.target.Name,
					"targetKey", key)
			}
		} else {
			// No delay - execute immediate restart
			if err := r.RestartDeployment(ctx, targetInfo.target, targetInfo.rule); err != nil {
				logger.Error(err, "Failed to restart deployment",
					"deployment", targetInfo.target.Name,
					"resource", resourceName,
					"resourceType", resourceType,
					"targetKey", key)

				// Record failed restart in status
				if statusErr := r.recordRestartEvent(ctx, targetInfo.rule, targetInfo.target, resourceName, resourceType, "Failed", err.Error()); statusErr != nil {
					logger.Error(statusErr, "Failed to record restart event")
				}

				continue
			}

			// Record successful restart in status
			if statusErr := r.recordRestartEvent(ctx, targetInfo.rule, targetInfo.target, resourceName, resourceType, "Success", ""); statusErr != nil {
				logger.Error(statusErr, "Failed to record restart event")
			}

			// Log the restart of the deployment
			logger.Info("Successfully restarted deployment",
				"deployment", targetInfo.target.Name,
				"resource", resourceName,
				"resourceType", resourceType,
				"targetKey", key,
				"namespace", targetInfo.target.Namespace)
		}
	}

	return nil
} // handleDelayedRestart handles the delayed restart of a target
func (r *BaseReconciler) handleDelayedRestart(ctx context.Context, target karov1alpha1.TargetSpec, rule *karov1alpha1.RestartRule, triggerInfo ResourceInfo, delay time.Duration) {
	logger := log.FromContext(ctx)

	// Wait for the delay period
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		// Delay period is over, execute restart
		logger.Info("Executing delayed restart",
			"target", target.Name,
			"delay", delay,
			"restartRule", rule.Name)

		if err := r.RestartDeployment(ctx, target, rule); err != nil {
			logger.Error(err, "Failed to restart deployment during delayed restart",
				"deployment", target.Name,
				"restartRule", rule.Name)

			// Record failed restart in status
			if statusErr := r.recordRestartEvent(ctx, rule, target, triggerInfo.Name, triggerInfo.Type, "Failed", err.Error()); statusErr != nil {
				logger.Error(statusErr, "Failed to record restart event")
			}
			return
		}

		// Record successful restart in status
		if statusErr := r.recordRestartEvent(ctx, rule, target, triggerInfo.Name, triggerInfo.Type, "Success", fmt.Sprintf("Delayed restart after %v", delay)); statusErr != nil {
			logger.Error(statusErr, "Failed to record restart event")
		}

		logger.Info("Successfully executed delayed restart",
			"deployment", target.Name,
			"restartRule", rule.Name,
			"delay", delay)

		// Remove from delay manager
		delayManager := GetDelayManager()
		delayManager.RemoveTarget(target, rule)

	case <-ctx.Done():
		// Context cancelled, cleanup
		logger.Info("Context cancelled, cleaning up delayed restart",
			"target", target.Name,
			"restartRule", rule.Name)

		delayManager := GetDelayManager()
		delayManager.RemoveTarget(target, rule)
	}
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
