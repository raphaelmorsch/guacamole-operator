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

// Desktop labels used by the DesktopPool / DesktopSession controllers.
const (
	DesktopLabelPool      = "desktop.guacamole.io/pool"
	DesktopLabelState     = "desktop.guacamole.io/state"
	DesktopLabelManagedBy = "desktop.guacamole.io/managed-by"
	DesktopLabelVM        = "desktop.guacamole.io/vm"
	DesktopLabelSession   = "desktop.guacamole.io/session"
	DesktopLabelRequester = "desktop.guacamole.io/requester"
	DesktopManagedByValue = "guacamole-operator"
)

// DesktopState represents the lifecycle state of a pooled desktop VM.
type DesktopState string

const (
	DesktopStateProvisioning DesktopState = "Provisioning"
	DesktopStateBooting      DesktopState = "Booting"
	DesktopStateAvailable    DesktopState = "Available"
	DesktopStateAllocated    DesktopState = "Allocated"
	DesktopStateInUse        DesktopState = "InUse"
	DesktopStateStopped      DesktopState = "Stopped"
	DesktopStateReleasing    DesktopState = "Releasing"
	DesktopStateDeleting     DesktopState = "Deleting"
	DesktopStateFailed       DesktopState = "Failed"
)

// Annotations used for desktop power management.
const (
	DesktopAnnotationAvailableSince = "desktop.guacamole.io/available-since"
	DesktopAnnotationPowerRequest   = "desktop.guacamole.io/power-request"

	DesktopPowerRequestWake    = "wake"
	DesktopPowerRequestSuspend = "suspend"
)

// DesktopPoolSpec defines the desired state of a desktop VM pool.
// MVP uses declarative replicas; minReady/bufferSize/maxSize are reserved for autoscaling.
type DesktopPoolSpec struct {
	// Replicas is the desired number of desktop VMs in the pool.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	Replicas *int32 `json:"replicas,omitempty"`

	// MinReady is the minimum number of Available desktops to keep warm.
	// Reserved for post-MVP autoscaling; ignored when replicas is set.
	// +optional
	// +kubebuilder:validation:Minimum=0
	MinReady *int32 `json:"minReady,omitempty"`

	// BufferSize is an extra warm-buffer beyond allocated/pending sessions.
	// Reserved for post-MVP autoscaling.
	// +optional
	// +kubebuilder:validation:Minimum=0
	BufferSize *int32 `json:"bufferSize,omitempty"`

	// MaxSize caps the total number of desktops when autoscaling is enabled.
	// +optional
	// +kubebuilder:validation:Minimum=1
	MaxSize *int32 `json:"maxSize,omitempty"`

	// Source selects the golden image used to clone desktop disks.
	Source DesktopPoolSourceSpec `json:"source"`

	// VirtualMachine configures CPU, memory and disk for each desktop VM.
	VirtualMachine DesktopPoolVMSpec `json:"virtualMachine"`

	// Network configures desktop network exposure (RDP Service).
	// +optional
	Network DesktopPoolNetworkSpec `json:"network,omitempty"`

	// Guacamole links the pool to a Guacamole instance and RDP credentials.
	Guacamole DesktopPoolGuacamoleSpec `json:"guacamole"`

	// RecyclePolicy controls what happens when a desktop is released.
	// Delete destroys the VM so the pool provisions a fresh clone.
	// +kubebuilder:default="Delete"
	// +kubebuilder:validation:Enum=Delete;Retain
	RecyclePolicy string `json:"recyclePolicy,omitempty"`

	// CreateConnections controls the provisional MVP strategy of creating a
	// GuacamoleConnection for every Available desktop.
	// Set to false when DesktopSession owns connection creation.
	// +kubebuilder:default=true
	CreateConnections *bool `json:"createConnections,omitempty"`

	// PowerManagement controls idle stop / wake-on-demand for pooled desktops.
	// +optional
	PowerManagement *DesktopPoolPowerManagementSpec `json:"powerManagement,omitempty"`
}

// DesktopPoolPowerManagementSpec configures idle stop and wake-on-demand.
type DesktopPoolPowerManagementSpec struct {
	// Enabled turns idle stop and wake-on-demand on.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// IdleSeconds is how long an Available desktop may sit unused before it is
	// stopped (KubeVirt runStrategy Halted). Default is 900 (15 minutes).
	// Zero stops Available desktops on the next reconcile (still respecting minReady).
	// +kubebuilder:default=900
	// +kubebuilder:validation:Minimum=0
	// +optional
	IdleSeconds *int64 `json:"idleSeconds,omitempty"`
}

// DesktopPoolSourceSpec references the golden image DataSource.
type DesktopPoolSourceSpec struct {
	// DataSource is a CDI DataSource (typically published by the golden-image pipeline).
	DataSource DesktopDataSourceRef `json:"dataSource"`
}

// DesktopDataSourceRef references a cdi.kubevirt.io DataSource.
type DesktopDataSourceRef struct {
	// Name of the DataSource.
	Name string `json:"name"`

	// Namespace of the DataSource.
	// Defaults to the DesktopPool namespace when unset.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// DesktopPoolVMSpec configures the VirtualMachine template for each desktop.
type DesktopPoolVMSpec struct {
	// StorageClassName used for the cloned root disk DataVolume.
	StorageClassName string `json:"storageClassName"`

	// DiskSize is the requested size of the root disk.
	// +kubebuilder:default="40Gi"
	DiskSize string `json:"diskSize,omitempty"`

	// CPU is the number of vCPU cores when InstanceType is unset.
	// +kubebuilder:default=2
	// +kubebuilder:validation:Minimum=1
	CPU *int32 `json:"cpu,omitempty"`

	// Memory is the memory request when InstanceType is unset.
	// +kubebuilder:default="4Gi"
	Memory string `json:"memory,omitempty"`

	// InstanceType optionally references a VirtualMachineClusterInstancetype
	// (for example u1.large). When set, cpu/memory are omitted from the VM domain.
	// +optional
	InstanceType string `json:"instanceType,omitempty"`
}

// DesktopPoolNetworkSpec configures RDP exposure for each desktop.
type DesktopPoolNetworkSpec struct {
	// RDPPort is the Service / guest RDP port.
	// +kubebuilder:default=3389
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	RDPPort *int32 `json:"rdpPort,omitempty"`
}

// DesktopPoolGuacamoleSpec links desktops to a Guacamole instance.
type DesktopPoolGuacamoleSpec struct {
	// InstanceRef is the Guacamole stack that will host connections.
	InstanceRef GuacamoleInstanceRef `json:"instanceRef"`

	// Username used for RDP authentication on pooled desktops.
	// Stored in the managed credentials Secret when password is set.
	// +kubebuilder:default="Administrator"
	Username string `json:"username,omitempty"`

	// Password is the Windows RDP password.
	// When set, the operator creates/updates a Secret in the DesktopPool namespace
	// (see credentialsSecretName) and wires GuacamoleConnections to it.
	// Prefer passwordSecretRef in production; plaintext is convenient for labs.
	// Ignored when passwordSecretRef is set.
	// +optional
	Password string `json:"password,omitempty"`

	// PasswordSecretRef references an existing Secret with the RDP password.
	// When set, the operator does not create a credentials Secret.
	// +optional
	PasswordSecretRef *SecretKeyRef `json:"passwordSecretRef,omitempty"`

	// CredentialsSecretName is the name of the Secret managed from password.
	// Defaults to "<desktoppool-name>-rdp-credentials".
	// +optional
	CredentialsSecretName string `json:"credentialsSecretName,omitempty"`

	// ParentGroup places generated GuacamoleConnections inside a connection group.
	// +optional
	ParentGroup string `json:"parentGroup,omitempty"`

	// IgnoreCert accepts self-signed RDP certificates (typical for golden images).
	// +kubebuilder:default=true
	IgnoreCert *bool `json:"ignoreCert,omitempty"`

	// Security mode for RDP sessions.
	// +kubebuilder:default="nla"
	// +kubebuilder:validation:Enum=any;nla;nla-ext;tls;vmconnect;rdp
	Security string `json:"security,omitempty"`
}

// DesktopPoolPhase is the high-level phase of the pool.
type DesktopPoolPhase string

const (
	DesktopPoolPhasePending  DesktopPoolPhase = "Pending"
	DesktopPoolPhaseReady    DesktopPoolPhase = "Ready"
	DesktopPoolPhaseScaling  DesktopPoolPhase = "Scaling"
	DesktopPoolPhaseFailed   DesktopPoolPhase = "Failed"
	DesktopPoolPhaseDeleting DesktopPoolPhase = "Deleting"
)

// DesktopMemberStatus reports one desktop belonging to the pool.
type DesktopMemberStatus struct {
	// Name of the VirtualMachine.
	Name string `json:"name"`

	// State is the desktop lifecycle state.
	State DesktopState `json:"state"`

	// ServiceDNS is the cluster DNS name for the RDP Service.
	// +optional
	ServiceDNS string `json:"serviceDNS,omitempty"`

	// ConnectionName is the GuacamoleConnection created for this desktop (MVP).
	// +optional
	ConnectionName string `json:"connectionName,omitempty"`

	// Message holds a human-readable detail for Failed states.
	// +optional
	Message string `json:"message,omitempty"`
}

// DesktopPoolStatus defines the observed state of DesktopPool.
type DesktopPoolStatus struct {
	// Phase is the current lifecycle phase of the pool.
	Phase DesktopPoolPhase `json:"phase,omitempty"`

	// Desired is the target number of desktops.
	Desired int32 `json:"desired,omitempty"`

	// Provisioning is the number of desktops still provisioning or booting.
	Provisioning int32 `json:"provisioning,omitempty"`

	// Available is the number of desktops ready to be allocated.
	Available int32 `json:"available,omitempty"`

	// Allocated is the number of desktops reserved by a session.
	Allocated int32 `json:"allocated,omitempty"`

	// Stopped is the number of desktops powered off by power management.
	Stopped int32 `json:"stopped,omitempty"`

	// Failed is the number of desktops in Failed state.
	Failed int32 `json:"failed,omitempty"`

	// DataSourceNamespace is the golden-image namespace where CDI clone RBAC is provisioned.
	// Resolved from spec.source.dataSource.namespace (defaults to the pool namespace).
	// +optional
	DataSourceNamespace string `json:"dataSourceNamespace,omitempty"`

	// CredentialsSecret is the Secret used for Windows RDP passwords on GuacamoleConnections.
	// Either the managed secret created from spec.guacamole.password, or the referenced passwordSecretRef.
	// +optional
	CredentialsSecret string `json:"credentialsSecret,omitempty"`

	// Desktops lists members currently managed by the pool.
	// +optional
	Desktops []DesktopMemberStatus `json:"desktops,omitempty"`

	// Conditions represent the latest available observations of the pool state.
	// Includes Ready and CloneAuthorized.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Desired",type=integer,JSONPath=`.status.desired`
// +kubebuilder:printcolumn:name="Available",type=integer,JSONPath=`.status.available`
// +kubebuilder:printcolumn:name="Stopped",type=integer,JSONPath=`.status.stopped`
// +kubebuilder:printcolumn:name="Allocated",type=integer,JSONPath=`.status.allocated`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DesktopPool manages a pool of Windows desktop VMs cloned from a golden DataSource
// and exposed to Guacamole over RDP.
type DesktopPool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DesktopPoolSpec   `json:"spec,omitempty"`
	Status DesktopPoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DesktopPoolList contains a list of DesktopPool.
type DesktopPoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DesktopPool `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DesktopPool{}, &DesktopPoolList{})
}
