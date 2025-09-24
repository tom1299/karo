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
	"context"
	"regexp"

	v2 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

type MemoryRestartRuleStore struct {
	// TODO: Use rules grouped by kind (Secret/ConfigMap) for faster lookup
	rules map[string]*karov1alpha1.RestartRule
}

func NewMemoryRestartRuleStore() *MemoryRestartRuleStore {
	return &MemoryRestartRuleStore{
		rules: make(map[string]*karov1alpha1.RestartRule),
	}
}

// Add inserts or updates a RestartRule.
func (s *MemoryRestartRuleStore) Add(ctx context.Context, rule *karov1alpha1.RestartRule) {
	s.rules[rule.Namespace+"/"+rule.Name] = rule
}

// Remove deletes a RestartRule by namespace and name.
func (s *MemoryRestartRuleStore) Remove(ctx context.Context, namespace, name string) {
	s.rules[namespace+"/"+name] = nil
}

func (s *MemoryRestartRuleStore) GetForSecret(ctx context.Context, secret v2.Secret, operation OperationType) []*karov1alpha1.RestartRule {
	return s.GetForKind(ctx, secret.ObjectMeta, v2.SchemeGroupVersion.WithKind("Secret").Kind, operation)
}

func (s *MemoryRestartRuleStore) GetForConfigMap(ctx context.Context, configmap v2.ConfigMap, operation OperationType) []*karov1alpha1.RestartRule {
	return s.GetForKind(ctx, configmap.ObjectMeta, v2.SchemeGroupVersion.WithKind("ConfigMap").Kind, operation)
}

func (s *MemoryRestartRuleStore) GetForKind(ctx context.Context, meta v1.ObjectMeta, kind string, operation OperationType) []*karov1alpha1.RestartRule {
	var matchedRules []*karov1alpha1.RestartRule
	for _, rule := range s.rules {
		if rule == nil {
			continue
		}
		if s.ruleMatchesResource(rule, meta, kind, operation) {
			matchedRules = append(matchedRules, rule)
		}
	}

	return matchedRules
}

func (s *MemoryRestartRuleStore) ruleMatchesResource(rule *karov1alpha1.RestartRule, meta v1.ObjectMeta, kind string, operation OperationType) bool {
	for _, change := range rule.Spec.Changes {
		if s.changeMatchesResource(change, meta, kind, operation) {
			return true
		}
	}

	return false
}

func (s *MemoryRestartRuleStore) changeMatchesResource(change karov1alpha1.ChangeSpec, meta v1.ObjectMeta, kind string, operation OperationType) bool {
	if change.Kind != kind {
		return false
	}

	if !s.matchesOperationType(change, operation) {
		return false
	}

	return s.matchesNameOrSelector(change, meta)
}

func (s *MemoryRestartRuleStore) matchesNameOrSelector(change karov1alpha1.ChangeSpec, meta v1.ObjectMeta) bool {
	if change.Name != "" {
		if change.IsRegex {
			// Use regex matching when IsRegex is true
			regex, err := regexp.Compile(change.Name)
			if err != nil {
				// If regex compilation fails, no match
				return false
			}

			return regex.MatchString(meta.Name)
		} else {
			// Use exact string matching when IsRegex is false (default)
			return change.Name == meta.Name
		}
	}

	if change.Selector != nil {
		selector, err := v1.LabelSelectorAsSelector(change.Selector)

		return err == nil && selector.Matches(labels.Set(meta.Labels))
	}

	return false
}

// matchesOperationType checks if the change spec's ChangeType includes the given operation
func (s *MemoryRestartRuleStore) matchesOperationType(change karov1alpha1.ChangeSpec, operation OperationType) bool {
	// If ChangeType is not specified, only "Update" operations match
	if len(change.ChangeType) == 0 {
		return operation == OperationUpdate
	}

	// Check if the operation is in the ChangeType array
	operationStr := string(operation)
	for _, changeType := range change.ChangeType {
		if changeType == operationStr {
			return true
		}
	}

	return false
}
