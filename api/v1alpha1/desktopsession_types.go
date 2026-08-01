/*
Copyright 2026.

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

// DesktopSessionSpec requests an exclusive desktop from a DesktopPool.
type DesktopSessionSpec struct {
	// PoolRef references the DesktopPool that should allocate a desktop.
	PoolRef LocalObjectReference `json:"poolRef"`

	// Requester identifies who requested the desktop.
	Requester DesktopSessionRequester `json:"requester"`

	// Priority controls broker queue order when the pool has no Available desktop.
	// Higher values are served first; equal priorities use FIFO (creationTimestamp).
	// +optional
	// +kubebuilder:default=0
	Priority *int32 `json:"priority,omitempty"`

	// MaxQueueSeconds fails the session if it waits in the broker queue longer
	// than this duration. Zero/unset means wait indefinitely.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxQueueSeconds *int64 `json:"maxQueueSeconds,omitempty"`

	// TTLSecondsAfterReady optionally bounds how long a Ready session may live
	// before the controller releases it. Zero/unset means no automatic expiry.
	// +optional
	// +kubebuilder:validation:Minimum=0
	TTLSecondsAfterReady *int64 `json:"ttlSecondsAfterReady,omitempty"`
}

// LocalObjectReference contains enough information to locate a namespaced object.
type LocalObjectReference struct {
	// Name of the referent.
	Name string `json:"name"`
}

// DesktopSessionRequester identifies the subject that requested the session.
type DesktopSessionRequester struct {
	// Subject is a free-form identity (username, email, SSO subject, etc.).
	Subject string `json:"subject"`
}

// DesktopSessionPhase is the lifecycle phase of a DesktopSession.
type DesktopSessionPhase string

const (
	DesktopSessionPhasePending  DesktopSessionPhase = "Pending"
	DesktopSessionPhaseQueued   DesktopSessionPhase = "Queued"
	DesktopSessionPhaseReady    DesktopSessionPhase = "Ready"
	DesktopSessionPhaseFailed   DesktopSessionPhase = "Failed"
	DesktopSessionPhaseReleased DesktopSessionPhase = "Released"
)

// DesktopSessionStatus defines the observed state of DesktopSession.
type DesktopSessionStatus struct {
	// Phase is the current lifecycle phase.
	Phase DesktopSessionPhase `json:"phase,omitempty"`

	// DesktopName is the VirtualMachine reserved for this session.
	// +optional
	DesktopName string `json:"desktopName,omitempty"`

	// ConnectionName is the GuacamoleConnection created for this session.
	// +optional
	ConnectionName string `json:"connectionName,omitempty"`

	// ServiceDNS is the RDP Service DNS for the allocated desktop.
	// +optional
	ServiceDNS string `json:"serviceDNS,omitempty"`

	// ReadyAt is when the session became Ready (used for TTL expiry).
	// +optional
	ReadyAt *metav1.Time `json:"readyAt,omitempty"`

	// QueuedAt is when the session entered the broker queue.
	// +optional
	QueuedAt *metav1.Time `json:"queuedAt,omitempty"`

	// QueuePosition is this session's 1-based place in the pool broker queue.
	// Only set while Phase=Queued.
	// +optional
	QueuePosition *int32 `json:"queuePosition,omitempty"`

	// QueueLength is how many sessions are waiting for the same pool.
	// +optional
	QueueLength *int32 `json:"queueLength,omitempty"`

	// Message holds a human-readable detail for Failed/Pending/Queued states.
	// +optional
	Message string `json:"message,omitempty"`

	// Conditions represent the latest observations of the session state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Pool",type=string,JSONPath=`.spec.poolRef.name`
// +kubebuilder:printcolumn:name="Subject",type=string,JSONPath=`.spec.requester.subject`
// +kubebuilder:printcolumn:name="Desktop",type=string,JSONPath=`.status.desktopName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Queue",type=string,JSONPath=`.status.queuePosition`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DesktopSession represents a request for an exclusive desktop from a DesktopPool.
// The controller reserves an Available VM, creates a GuacamoleConnection, and on
// release/delete returns capacity to the pool (recyclePolicy=Delete destroys the VM).
type DesktopSession struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DesktopSessionSpec   `json:"spec,omitempty"`
	Status DesktopSessionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DesktopSessionList contains a list of DesktopSession.
type DesktopSessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DesktopSession `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DesktopSession{}, &DesktopSessionList{})
}
