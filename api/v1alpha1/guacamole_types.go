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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GuacamoleSpec defines the desired state of a Guacamole stack.
type GuacamoleSpec struct {
	// GuacamoleImage is the container image for the Guacamole web application.
	// +kubebuilder:default="guacamole/guacamole:1.6.0"
	GuacamoleImage string `json:"guacamoleImage,omitempty"`

	// GuacdImage is the container image for the guacd proxy daemon.
	// +kubebuilder:default="guacamole/guacd:1.6.0"
	GuacdImage string `json:"guacdImage,omitempty"`

	// MySQLImage is the container image for the MySQL database.
	// +kubebuilder:default="mysql:8.0"
	MySQLImage string `json:"mysqlImage,omitempty"`

	// Replicas is the number of Guacamole web application replicas.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// GuacdReplicas is the number of guacd replicas.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	GuacdReplicas *int32 `json:"guacdReplicas,omitempty"`

	// Database holds MySQL credentials and storage configuration.
	// +optional
	Database DatabaseSpec `json:"database,omitempty"`

	// Route configures OpenShift Route exposure for the Guacamole web UI.
	// +optional
	Route RouteSpec `json:"route,omitempty"`

	// LogLevel sets the log level for Guacamole and guacd.
	// +kubebuilder:default="info"
	// +kubebuilder:validation:Enum=debug;info;warn;error
	LogLevel string `json:"logLevel,omitempty"`

	// Resources defines resource requests and limits for Guacamole containers.
	// +optional
	Resources GuacamoleResources `json:"resources,omitempty"`
}

// DatabaseSpec configures the MySQL backend used by Guacamole.
type DatabaseSpec struct {
	// User is the MySQL application user.
	// +kubebuilder:default="guacamole_user"
	User string `json:"user,omitempty"`

	// Password is the MySQL application password.
	// +kubebuilder:default="guacamole_pass"
	Password string `json:"password,omitempty"`

	// RootPassword is the MySQL root password.
	// +kubebuilder:default="rootpass123"
	RootPassword string `json:"rootPassword,omitempty"`

	// Name is the MySQL database name.
	// +kubebuilder:default="guacamole_db"
	Name string `json:"name,omitempty"`

	// StorageSize is the size of the persistent volume for MySQL data.
	// +kubebuilder:default="5Gi"
	StorageSize string `json:"storageSize,omitempty"`

	// StorageClassName optionally selects a StorageClass for the MySQL PVC.
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`
}

// RouteSpec configures OpenShift Route exposure.
type RouteSpec struct {
	// Enabled creates an OpenShift Route for the Guacamole web UI.
	// +kubebuilder:default=true
	Enabled *bool `json:"enabled,omitempty"`

	// Hostname sets a custom hostname for the Route.
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// TLSTermination defines the TLS termination mode (edge, passthrough, reencrypt).
	// +kubebuilder:default="edge"
	// +kubebuilder:validation:Enum=edge;passthrough;reencrypt
	TLSTermination string `json:"tlsTermination,omitempty"`
}

// GuacamoleResources defines resource requirements for the stack components.
type GuacamoleResources struct {
	// Guacamole resource requirements for the web application container.
	// +optional
	Guacamole corev1.ResourceRequirements `json:"guacamole,omitempty"`

	// Guacd resource requirements for the guacd container.
	// +optional
	Guacd corev1.ResourceRequirements `json:"guacd,omitempty"`

	// MySQL resource requirements for the database container.
	// +optional
	MySQL corev1.ResourceRequirements `json:"mysql,omitempty"`
}

// GuacamolePhase represents the high-level lifecycle phase of the instance.
type GuacamolePhase string

const (
	GuacamolePhasePending GuacamolePhase = "Pending"
	GuacamolePhaseRunning GuacamolePhase = "Running"
	GuacamolePhaseFailed  GuacamolePhase = "Failed"
)

// GuacamoleStatus defines the observed state of Guacamole.
type GuacamoleStatus struct {
	// Phase is the current lifecycle phase of the Guacamole instance.
	Phase GuacamolePhase `json:"phase,omitempty"`

	// RouteURL is the external URL when a Route is exposed.
	RouteURL string `json:"routeURL,omitempty"`

	// Conditions represent the latest available observations of the instance state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Route",type=string,JSONPath=`.status.routeURL`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Guacamole is the Schema for the guacamoles API.
type Guacamole struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GuacamoleSpec   `json:"spec,omitempty"`
	Status GuacamoleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GuacamoleList contains a list of Guacamole.
type GuacamoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Guacamole `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Guacamole{}, &GuacamoleList{})
}
