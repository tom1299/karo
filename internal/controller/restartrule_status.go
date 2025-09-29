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

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

// validTransitions represents the allowed phase transitions for a RestartRule.
var validTransitions = map[string][]string{
	"":        {"Pending"},
	"Pending": {"Active", "Invalid"},
	"Active":  {"Failed"},
}

var errInvalidPhaseTransition = errors.New("invalid phase transition")

// isValidTransition checks if a phase transition is valid.
func isValidTransition(from, to string) bool {
	allowed, ok := validTransitions[from]
	if !ok {
		return false
	}
	for _, a := range allowed {
		if a == to {
			return true
		}
	}

	return false
}

// updateStatus updates the status of a RestartRule, ensuring valid phase transitions.
func (r *RestartRuleReconciler) updateStatus(ctx context.Context, rule *karov1alpha1.RestartRule, newPhase string, reason string, message string) error {

	if !isValidTransition(rule.Status.Phase, newPhase) {
		return fmt.Errorf("%w: from %q to %q", errInvalidPhaseTransition, rule.Status.Phase, newPhase)
	}

	rule.Status.Phase = newPhase
	rule.Status.LastProcessedAt = &metav1.Time{Time: time.Now()}

	readyStatus := metav1.ConditionFalse
	if newPhase == "Active" {
		readyStatus = metav1.ConditionTrue
	}

	meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             readyStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: rule.Generation,
	})

	validStatus := metav1.ConditionTrue
	if newPhase == "Invalid" {
		validStatus = metav1.ConditionFalse
	}

	meta.SetStatusCondition(&rule.Status.Conditions, metav1.Condition{
		Type:               "Valid",
		Status:             validStatus,
		Reason:             reason,
		Message:            message,
		ObservedGeneration: rule.Generation,
	})

	// return r.Status().Patch(ctx, rule, client.MergeFrom(rule.DeepCopy()))
	return r.Status().Update(ctx, rule)
}
