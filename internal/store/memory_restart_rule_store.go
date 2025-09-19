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

func (s *MemoryRestartRuleStore) GetForSecret(ctx context.Context, secret v2.Secret) []*karov1alpha1.RestartRule {
	return s.GetForKind(ctx, secret.ObjectMeta, v2.SchemeGroupVersion.WithKind("Secret").Kind)
}

func (s *MemoryRestartRuleStore) GetForConfigMap(ctx context.Context, configmap v2.ConfigMap) []*karov1alpha1.RestartRule {
	return s.GetForKind(ctx, configmap.ObjectMeta, v2.SchemeGroupVersion.WithKind("ConfigMap").Kind)
}

func (s *MemoryRestartRuleStore) GetForKind(ctx context.Context, meta v1.ObjectMeta, kind string) []*karov1alpha1.RestartRule {
	var matchedRules []*karov1alpha1.RestartRule
	for _, rule := range s.rules {
		if rule == nil {
			continue
		}
		for _, change := range rule.Spec.Changes {
			if change.Kind != kind {
				continue
			}
			if change.Name != "" && change.Name == meta.Name {
				matchedRules = append(matchedRules, rule)
			}
			if change.Selector != nil {
				selector, err := v1.LabelSelectorAsSelector(change.Selector)
				if err == nil && selector.Matches(labels.Set(meta.Labels)) {
					matchedRules = append(matchedRules, rule)
				}
			}
		}
	}
	return matchedRules
}
