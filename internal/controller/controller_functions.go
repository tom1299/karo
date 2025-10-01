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
	"errors"
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

var (
	// ErrUnsupportedTargetKind is returned when an unsupported target kind is encountered
	ErrUnsupportedTargetKind = errors.New("unsupported target kind")
)

// BaseReconciler contains common fields and methods for resource controllers
type BaseReconciler struct {
	client.Client

	RestartRuleStore      store.RestartRuleStore
	DelayedRestartManager DelayedRestartManager
	operationType         store.OperationType
}

// targetKey uniquely identifies a deployment target
type targetKey struct {
	kind      string
	name      string
	namespace string
}

// ResourceInfo contains information about a Kubernetes resource
type ResourceInfo struct {
	Name      string
	Namespace string
	Type      string
}

// RestartDeployment performs a rollout restart of the target deployment or statefulset
func (r *BaseReconciler) RestartDeployment(ctx context.Context, target karov1alpha1.TargetSpec, rule *karov1alpha1.RestartRule) error {
	targetNamespace := target.Namespace
	if targetNamespace == "" {
		targetNamespace = rule.Namespace
	}

	objectKey := types.NamespacedName{
		Name:      target.Name,
		Namespace: targetNamespace,
	}

	// Handle different resource types
	switch target.Kind {
	case "Deployment":
		return r.restartDeployment(ctx, objectKey, targetNamespace, target.Name)
	case "StatefulSet":
		return r.restartStatefulSet(ctx, objectKey, targetNamespace, target.Name)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedTargetKind, target.Kind)
	}
}

// ProcessRestartRules processes restart rules for deployments
func (r *BaseReconciler) ProcessRestartRules(ctx context.Context, restartRules []*karov1alpha1.RestartRule, resourceName, resourceType string) error {
	logger := log.FromContext(ctx)

	// Group restart rules by target
	rulesByTarget := r.groupRulesByTarget(restartRules)

	// Process each unique target
	for tgtKey, rules := range rulesByTarget {
		maxDelay, maxDelayRule := r.findMaxDelay(rules)
		targetSpec := r.getTargetSpec(tgtKey, rules)
		ruleNames := r.extractRuleNames(rules)

		r.logProcessingTarget(logger, tgtKey, resourceName, resourceType, ruleNames)

		if maxDelay != nil && *maxDelay > 0 {
			r.processDelayedRestart(ctx, logger, tgtKey, targetSpec, rules, ruleNames, maxDelay, maxDelayRule, resourceName, resourceType)
		} else if targetSpec.Kind == "Deployment" || targetSpec.Kind == "StatefulSet" {
			r.processImmediateRestart(ctx, logger, targetSpec, rules, ruleNames, resourceName, resourceType)
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

// restartDeployment restarts a deployment by updating its pod template annotation
func (r *BaseReconciler) restartDeployment(ctx context.Context, objectKey types.NamespacedName, namespace, name string) error {
	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, objectKey, deployment); err != nil {
		return fmt.Errorf("failed to get deployment %s/%s: %w", namespace, name, err)
	}

	r.addRestartAnnotation(&deployment.Spec.Template.Annotations)

	if err := r.Update(ctx, deployment); err != nil {
		return fmt.Errorf("failed to update deployment %s/%s: %w", namespace, name, err)
	}

	return nil
}

// restartStatefulSet restarts a statefulset by updating its pod template annotation
func (r *BaseReconciler) restartStatefulSet(ctx context.Context, objectKey types.NamespacedName, namespace, name string) error {
	statefulSet := &appsv1.StatefulSet{}
	if err := r.Get(ctx, objectKey, statefulSet); err != nil {
		return fmt.Errorf("failed to get statefulset %s/%s: %w", namespace, name, err)
	}

	r.addRestartAnnotation(&statefulSet.Spec.Template.Annotations)

	if err := r.Update(ctx, statefulSet); err != nil {
		return fmt.Errorf("failed to update statefulset %s/%s: %w", namespace, name, err)
	}

	return nil
}

// addRestartAnnotation adds the restart timestamp annotation to the given annotations map
func (r *BaseReconciler) addRestartAnnotation(annotations *map[string]string) {
	if *annotations == nil {
		*annotations = make(map[string]string)
	}
	(*annotations)["karo.jeeatwork.com/restartedAt"] = time.Now().Format(time.RFC3339)
}

// groupRulesByTarget groups restart rules by their target deployment
func (r *BaseReconciler) groupRulesByTarget(restartRules []*karov1alpha1.RestartRule) map[targetKey][]*karov1alpha1.RestartRule {
	rulesByTarget := make(map[targetKey][]*karov1alpha1.RestartRule)

	for _, rule := range restartRules {
		for _, target := range rule.Spec.Targets {
			targetNamespace := target.Namespace
			if targetNamespace == "" {
				targetNamespace = rule.Namespace
			}

			key := targetKey{
				kind:      target.Kind,
				name:      target.Name,
				namespace: targetNamespace,
			}

			rulesByTarget[key] = append(rulesByTarget[key], rule)
		}
	}

	return rulesByTarget
}

// findMaxDelay finds the maximum delay among all rules
func (r *BaseReconciler) findMaxDelay(rules []*karov1alpha1.RestartRule) (*int32, *karov1alpha1.RestartRule) {
	var maxDelay *int32
	var maxDelayRule *karov1alpha1.RestartRule

	for _, rule := range rules {
		if rule.Spec.DelayRestart != nil {
			if maxDelay == nil || *rule.Spec.DelayRestart > *maxDelay {
				maxDelay = rule.Spec.DelayRestart
				maxDelayRule = rule
			}
		}
	}

	return maxDelay, maxDelayRule
}

// getTargetSpec extracts the target spec for a given target key
func (r *BaseReconciler) getTargetSpec(tgtKey targetKey, rules []*karov1alpha1.RestartRule) karov1alpha1.TargetSpec {
	firstRule := rules[0]
	var targetSpec karov1alpha1.TargetSpec

	for _, target := range firstRule.Spec.Targets {
		targetNS := target.Namespace
		if targetNS == "" {
			targetNS = firstRule.Namespace
		}
		if target.Kind == tgtKey.kind && target.Name == tgtKey.name && targetNS == tgtKey.namespace {
			targetSpec = target

			break
		}
	}

	return targetSpec
}

// extractRuleNames extracts rule names for logging
func (r *BaseReconciler) extractRuleNames(rules []*karov1alpha1.RestartRule) []string {
	ruleNames := make([]string, len(rules))
	for i, rule := range rules {
		ruleNames[i] = rule.Name
	}

	return ruleNames
}

// logProcessingTarget logs information about the target being processed
func (r *BaseReconciler) logProcessingTarget(logger logr.Logger, tgtKey targetKey, resourceName, resourceType string, ruleNames []string) {
	logger.Info("Processing restart for target",
		"target", fmt.Sprintf("%s/%s/%s", tgtKey.kind, tgtKey.namespace, tgtKey.name),
		"resource", resourceName,
		"resourceType", resourceType,
		"restartRules", ruleNames,
		"ruleCount", len(ruleNames))
}

// processDelayedRestart handles delayed restart logic
func (r *BaseReconciler) processDelayedRestart(ctx context.Context, logger logr.Logger, tgtKey targetKey, targetSpec karov1alpha1.TargetSpec, rules []*karov1alpha1.RestartRule, ruleNames []string, maxDelay *int32, maxDelayRule *karov1alpha1.RestartRule, resourceName, resourceType string) {
	delay := time.Duration(*maxDelay) * time.Second

	targetKey := TargetKey{
		Kind:      tgtKey.kind,
		Name:      tgtKey.name,
		Namespace: tgtKey.namespace,
	}

	restartFunc := r.createRestartFunc(ctx, logger, targetSpec, rules, ruleNames, targetKey, resourceName, resourceType)

	finalDelay, isNew, err := r.DelayedRestartManager.ScheduleRestart(ctx, targetKey, delay, restartFunc)
	if err != nil {
		r.handleScheduleError(ctx, logger, err, targetKey, delay, targetSpec, rules, resourceName, resourceType)

		return
	}

	r.logScheduleResult(logger, isNew, targetKey, finalDelay, delay, resourceName, resourceType, ruleNames, maxDelayRule)
}

// createRestartFunc creates the restart function for delayed execution
func (r *BaseReconciler) createRestartFunc(_ context.Context, logger logr.Logger, targetSpec karov1alpha1.TargetSpec, rules []*karov1alpha1.RestartRule, ruleNames []string, targetKey TargetKey, resourceName, resourceType string) RestartFunc {
	firstRule := rules[0]

	return func(taskCtx context.Context) error {
		if err := r.RestartDeployment(taskCtx, targetSpec, firstRule); err != nil {
			logger.Error(err, "Failed to execute delayed restart",
				"target", targetKey.String(),
				"resource", resourceName,
				"resourceType", resourceType)

			r.recordRestartEventsForRules(taskCtx, logger, rules, targetSpec, resourceName, resourceType, "Failed", err.Error())

			return err
		}

		r.recordRestartEventsForRules(taskCtx, logger, rules, targetSpec, resourceName, resourceType, "Success", "")

		logger.Info("Successfully executed delayed restart",
			"target", targetKey.String(),
			"resource", resourceName,
			"resourceType", resourceType,
			"restartRules", ruleNames)

		return nil
	}
}

// handleScheduleError handles errors from scheduling delayed restarts
func (r *BaseReconciler) handleScheduleError(ctx context.Context, logger logr.Logger, err error, targetKey TargetKey, delay time.Duration, targetSpec karov1alpha1.TargetSpec, rules []*karov1alpha1.RestartRule, resourceName, resourceType string) {
	logger.Error(err, "Failed to schedule delayed restart",
		"target", targetKey.String(),
		"requestedDelay", delay)

	r.recordRestartEventsForRules(ctx, logger, rules, targetSpec, resourceName, resourceType, "Failed", fmt.Sprintf("Failed to schedule delayed restart: %v", err))
}

// logScheduleResult logs the result of scheduling a delayed restart
func (r *BaseReconciler) logScheduleResult(logger logr.Logger, isNew bool, targetKey TargetKey, finalDelay, requestedDelay time.Duration, resourceName, resourceType string, ruleNames []string, maxDelayRule *karov1alpha1.RestartRule) {
	if !isNew {
		logger.Info("Target already has pending delayed restart, skipping duplicate schedule",
			"target", targetKey.String(),
			"existingDelay", finalDelay,
			"requestedDelay", requestedDelay,
			"resource", resourceName,
			"resourceType", resourceType,
			"restartRules", ruleNames)
	} else {
		logger.Info("Scheduled delayed restart",
			"target", targetKey.String(),
			"delay", finalDelay,
			"resource", resourceName,
			"resourceType", resourceType,
			"restartRules", ruleNames,
			"maxDelayFromRule", maxDelayRule.Name)
	}
}

// processImmediateRestart handles immediate restart logic
func (r *BaseReconciler) processImmediateRestart(ctx context.Context, logger logr.Logger, targetSpec karov1alpha1.TargetSpec, rules []*karov1alpha1.RestartRule, ruleNames []string, resourceName, resourceType string) {
	firstRule := rules[0]

	if err := r.RestartDeployment(ctx, targetSpec, firstRule); err != nil {
		logger.Error(err, "Failed to restart deployment",
			"deployment", targetSpec.Name,
			"resource", resourceName,
			"resourceType", resourceType,
			"restartRules", ruleNames)

		r.recordRestartEventsForRules(ctx, logger, rules, targetSpec, resourceName, resourceType, "Failed", err.Error())

		return
	}

	r.recordRestartEventsForRules(ctx, logger, rules, targetSpec, resourceName, resourceType, "Success", "")

	logger.Info("Successfully restarted deployment immediately",
		"deployment", targetSpec.Name,
		"resource", resourceName,
		"resourceType", resourceType,
		"restartRules", ruleNames,
		"namespace", targetSpec.Namespace)
}

// recordRestartEventsForRules records restart events for all rules
func (r *BaseReconciler) recordRestartEventsForRules(ctx context.Context, logger logr.Logger, rules []*karov1alpha1.RestartRule, targetSpec karov1alpha1.TargetSpec, resourceName, resourceType, status, message string) {
	for _, rule := range rules {
		if statusErr := r.recordRestartEvent(ctx, rule, targetSpec, resourceName, resourceType, status, message); statusErr != nil {
			logger.Error(statusErr, "Failed to record restart event")
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
