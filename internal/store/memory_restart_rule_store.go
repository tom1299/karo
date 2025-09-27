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
	"fmt"
	"regexp"
	"sync"
	"time"

	v2 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

type MemoryRestartRuleStore struct {
	// TODO: Use rules grouped by kind (Secret/ConfigMap) for faster lookup
	rules map[string]*karov1alpha1.RestartRule

	// Delayed restart tracking
	delayedRestarts map[string]DelayedRestart // key: workloadKind/workloadKey
	mu              sync.RWMutex              // protects concurrent access
}

func NewMemoryRestartRuleStore() *MemoryRestartRuleStore {
	return &MemoryRestartRuleStore{
		rules:           make(map[string]*karov1alpha1.RestartRule),
		delayedRestarts: make(map[string]DelayedRestart),
	}
}

// Add inserts or updates a RestartRule.
func (s *MemoryRestartRuleStore) Add(ctx context.Context, rule *karov1alpha1.RestartRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[rule.Namespace+"/"+rule.Name] = rule
}

// Remove deletes a RestartRule by namespace and name.
func (s *MemoryRestartRuleStore) Remove(ctx context.Context, namespace, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules[namespace+"/"+name] = nil
}

func (s *MemoryRestartRuleStore) GetForSecret(ctx context.Context, secret v2.Secret, operation OperationType) []*karov1alpha1.RestartRule {
	return s.GetForKind(ctx, secret.ObjectMeta, v2.SchemeGroupVersion.WithKind("Secret").Kind, operation)
}

func (s *MemoryRestartRuleStore) GetForConfigMap(ctx context.Context, configmap v2.ConfigMap, operation OperationType) []*karov1alpha1.RestartRule {
	return s.GetForKind(ctx, configmap.ObjectMeta, v2.SchemeGroupVersion.WithKind("ConfigMap").Kind, operation)
}

func (s *MemoryRestartRuleStore) GetForKind(ctx context.Context, meta v1.ObjectMeta, kind string, operation OperationType) []*karov1alpha1.RestartRule {
	s.mu.RLock()
	defer s.mu.RUnlock()

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

// delayKey creates a consistent key for delayed restart tracking
func (s *MemoryRestartRuleStore) delayKey(workloadKey, workloadKind string) string {
	return fmt.Sprintf("%s/%s", workloadKind, workloadKey)
}

// IsWorkloadDelayed checks if a workload is currently scheduled for delayed restart
func (s *MemoryRestartRuleStore) IsWorkloadDelayed(ctx context.Context, workloadKey, workloadKind string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := s.delayKey(workloadKey, workloadKind)
	delayedRestart, exists := s.delayedRestarts[key]
	if !exists {
		return false
	}

	// Check if the delay has expired
	if time.Now().After(delayedRestart.RestartAt) {
		// Delay has expired, remove it
		delete(s.delayedRestarts, key)

		return false
	}

	return true
}

// AddDelayedRestart schedules a workload for delayed restart
// If the workload is already delayed, it extends the delay if the new delay is longer
func (s *MemoryRestartRuleStore) AddDelayedRestart(ctx context.Context, workloadKey, workloadKind string, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.delayKey(workloadKey, workloadKind)
	now := time.Now()
	newRestartAt := now.Add(delay)

	// Check if there's already a delayed restart for this workload
	if existingDelay, exists := s.delayedRestarts[key]; exists {
		// Use the longer delay (later restart time)
		if newRestartAt.After(existingDelay.RestartAt) {
			s.delayedRestarts[key] = DelayedRestart{
				WorkloadKey:  workloadKey,
				WorkloadKind: workloadKind,
				ScheduledAt:  now,
				RestartAt:    newRestartAt,
				Delay:        delay,
			}
		}
		// If the existing delay is longer, keep it
		return
	}

	// Add new delayed restart
	s.delayedRestarts[key] = DelayedRestart{
		WorkloadKey:  workloadKey,
		WorkloadKind: workloadKind,
		ScheduledAt:  now,
		RestartAt:    newRestartAt,
		Delay:        delay,
	}
}

// GetDelayedRestarts returns all currently scheduled delayed restarts
func (s *MemoryRestartRuleStore) GetDelayedRestarts(ctx context.Context) []DelayedRestart {
	s.mu.Lock()
	defer s.mu.Unlock()

	var delayedRestarts []DelayedRestart
	now := time.Now()

	// Clean up expired delays and return active ones
	for key, delayedRestart := range s.delayedRestarts {
		if now.After(delayedRestart.RestartAt) {
			// Delay has expired, remove it
			delete(s.delayedRestarts, key)
		} else {
			delayedRestarts = append(delayedRestarts, delayedRestart)
		}
	}

	return delayedRestarts
}

// RemoveDelayedRestart removes a delayed restart for the specified workload
func (s *MemoryRestartRuleStore) RemoveDelayedRestart(ctx context.Context, workloadKey, workloadKind string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.delayKey(workloadKey, workloadKind)
	delete(s.delayedRestarts, key)
}
