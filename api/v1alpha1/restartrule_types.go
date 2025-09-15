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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// RestartRuleSpec defines the desired state of RestartRule
type RestartRuleSpec struct {
	// When defines the trigger conditions for the restart rule
	// +required
	When TriggerSpec `json:"when"`

	// Then defines the actions to take when the trigger conditions are met
	// +required
	Then ActionSpec `json:"then"`

	// Conditions defines additional conditions that must be met for the rule to apply
	// +optional
	Conditions []ConditionSpec `json:"conditions,omitempty"`
}

// TriggerSpec defines what triggers a restart
type TriggerSpec struct {
	// ConfigMapChange specifies the name of a ConfigMap whose changes should trigger a restart
	// +optional
	ConfigMapChange string `json:"configMapChange,omitempty"`
}

// ActionSpec defines what action to take when triggered
type ActionSpec struct {
	// Restart specifies the resource to restart in format 'kind/name'
	// +required
	Restart string `json:"restart"`
}

// ConditionSpec defines additional conditions for the rule to apply
type ConditionSpec struct {
	// Namespace restricts the rule to a specific namespace
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// RestartRuleStatus defines the observed state of RestartRule.
type RestartRuleStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// conditions represent the current state of the RestartRule resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// RestartRule is the Schema for the restartrules API
type RestartRule struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec defines the desired state of RestartRule
	// +required
	Spec RestartRuleSpec `json:"spec"`

	// status defines the observed state of RestartRule
	// +optional
	Status RestartRuleStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// RestartRuleList contains a list of RestartRule
type RestartRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RestartRule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RestartRule{}, &RestartRuleList{})
}
