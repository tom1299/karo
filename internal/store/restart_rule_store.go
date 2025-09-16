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

package store

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

// RestartRuleStore defines methods for managing RestartRules.
type RestartRuleStore interface {
	// Add inserts or updates a RestartRule.
	Add(rule *karov1alpha1.RestartRule)

	// Remove deletes a RestartRule by namespace and name.
	Remove(namespace, name string)

	// GetByName retrieves RestartRules by namespace and resource name.
	GetByName(kind, namespace, name string) []*karov1alpha1.RestartRule

	// GetBySelector retrieves RestartRules by namespace and label selector.
	GetBySelector(kind, namespace string, selector *metav1.LabelSelector) []*karov1alpha1.RestartRule
}
