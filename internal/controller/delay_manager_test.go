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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	karov1alpha1 "karo.jeeatwork.com/api/v1alpha1"
)

func TestDelayManager_ScheduleRestart(t *testing.T) {
	tests := []struct {
		name           string
		targets        []karov1alpha1.TargetSpec
		rules          []*karov1alpha1.RestartRule
		delays         []time.Duration
		expectSchedule []bool
	}{
		{
			name: "single target scheduled successfully",
			targets: []karov1alpha1.TargetSpec{
				{Kind: "Deployment", Name: "app1"},
			},
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
				},
			},
			delays:         []time.Duration{5 * time.Second},
			expectSchedule: []bool{true},
		},
		{
			name: "duplicate target not scheduled",
			targets: []karov1alpha1.TargetSpec{
				{Kind: "Deployment", Name: "app1"},
				{Kind: "Deployment", Name: "app1"},
			},
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule2", Namespace: "default"},
				},
			},
			delays:         []time.Duration{5 * time.Second, 10 * time.Second},
			expectSchedule: []bool{true, false},
		},
		{
			name: "different targets scheduled separately",
			targets: []karov1alpha1.TargetSpec{
				{Kind: "Deployment", Name: "app1"},
				{Kind: "Deployment", Name: "app2"},
			},
			rules: []*karov1alpha1.RestartRule{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "rule2", Namespace: "default"},
				},
			},
			delays:         []time.Duration{5 * time.Second, 10 * time.Second},
			expectSchedule: []bool{true, true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a new delay manager for each test
			dm := &DelayManager{
				restarts: make(map[string]*DelayedRestart),
			}

			ctx := context.Background()
			triggerInfo := ResourceInfo{
				Name:      "test-configmap",
				Namespace: "default",
				Type:      "ConfigMap",
			}

			for i, target := range tt.targets {
				result := dm.ScheduleRestart(ctx, target, tt.rules[i], triggerInfo, tt.delays[i])
				if result != tt.expectSchedule[i] {
					t.Errorf("ScheduleRestart() for target %d = %v, want %v", i, result, tt.expectSchedule[i])
				}
			}
		})
	}
}

func TestDelayManager_IsTargetScheduled(t *testing.T) {
	dm := &DelayManager{
		restarts: make(map[string]*DelayedRestart),
	}

	ctx := context.Background()
	target := karov1alpha1.TargetSpec{Kind: "Deployment", Name: "app1"}
	rule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
	}
	triggerInfo := ResourceInfo{
		Name:      "test-configmap",
		Namespace: "default",
		Type:      "ConfigMap",
	}

	// Initially not scheduled
	if dm.IsTargetScheduled(target, rule) {
		t.Error("IsTargetScheduled() = true, want false for unscheduled target")
	}

	// Schedule target
	dm.ScheduleRestart(ctx, target, rule, triggerInfo, 5*time.Second)

	// Should now be scheduled
	if !dm.IsTargetScheduled(target, rule) {
		t.Error("IsTargetScheduled() = false, want true for scheduled target")
	}
}

func TestDelayManager_GetDueRestarts(t *testing.T) {
	dm := &DelayManager{
		restarts: make(map[string]*DelayedRestart),
	}

	ctx := context.Background()
	target1 := karov1alpha1.TargetSpec{Kind: "Deployment", Name: "app1"}
	target2 := karov1alpha1.TargetSpec{Kind: "Deployment", Name: "app2"}
	rule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
	}
	triggerInfo := ResourceInfo{
		Name:      "test-configmap",
		Namespace: "default",
		Type:      "ConfigMap",
	}

	// Schedule one target with very short delay (should be due)
	dm.ScheduleRestart(ctx, target1, rule, triggerInfo, 1*time.Millisecond)
	// Schedule another with longer delay (should not be due)
	dm.ScheduleRestart(ctx, target2, rule, triggerInfo, 1*time.Hour)

	// Wait for first target to be due
	time.Sleep(10 * time.Millisecond)

	dueRestarts := dm.GetDueRestarts()

	if len(dueRestarts) != 1 {
		t.Errorf("GetDueRestarts() returned %d restarts, want 1", len(dueRestarts))
	}

	if len(dueRestarts) > 0 && dueRestarts[0].Target.Name != "app1" {
		t.Errorf("GetDueRestarts() returned target %s, want app1", dueRestarts[0].Target.Name)
	}

	// Verify target1 is no longer scheduled
	if dm.IsTargetScheduled(target1, rule) {
		t.Error("IsTargetScheduled() = true, want false for target that was returned as due")
	}

	// Verify target2 is still scheduled
	if !dm.IsTargetScheduled(target2, rule) {
		t.Error("IsTargetScheduled() = false, want true for target that should still be scheduled")
	}
}

func TestDelayManager_RemoveTarget(t *testing.T) {
	dm := &DelayManager{
		restarts: make(map[string]*DelayedRestart),
	}

	ctx := context.Background()
	target := karov1alpha1.TargetSpec{Kind: "Deployment", Name: "app1"}
	rule := &karov1alpha1.RestartRule{
		ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"},
	}
	triggerInfo := ResourceInfo{
		Name:      "test-configmap",
		Namespace: "default",
		Type:      "ConfigMap",
	}

	// Schedule target
	dm.ScheduleRestart(ctx, target, rule, triggerInfo, 5*time.Second)

	// Verify it's scheduled
	if !dm.IsTargetScheduled(target, rule) {
		t.Error("IsTargetScheduled() = false, want true for scheduled target")
	}

	// Remove target
	dm.RemoveTarget(target, rule)

	// Verify it's no longer scheduled
	if dm.IsTargetScheduled(target, rule) {
		t.Error("IsTargetScheduled() = true, want false after RemoveTarget")
	}
}

func TestDelayManager_getTargetKey(t *testing.T) {
	dm := &DelayManager{
		restarts: make(map[string]*DelayedRestart),
	}

	tests := []struct {
		name        string
		target      karov1alpha1.TargetSpec
		rule        *karov1alpha1.RestartRule
		expectedKey string
	}{
		{
			name:        "target with namespace",
			target:      karov1alpha1.TargetSpec{Kind: "Deployment", Name: "app1", Namespace: "test"},
			rule:        &karov1alpha1.RestartRule{ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"}},
			expectedKey: "test/Deployment/app1",
		},
		{
			name:        "target without namespace uses rule namespace",
			target:      karov1alpha1.TargetSpec{Kind: "Deployment", Name: "app1"},
			rule:        &karov1alpha1.RestartRule{ObjectMeta: metav1.ObjectMeta{Name: "rule1", Namespace: "default"}},
			expectedKey: "default/Deployment/app1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := dm.getTargetKey(tt.target, tt.rule)
			if key != tt.expectedKey {
				t.Errorf("getTargetKey() = %s, want %s", key, tt.expectedKey)
			}
		})
	}
}
