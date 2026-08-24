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

	// Autoscaling configures horizontal pod autoscaling for the Guacamole web deployment.
	// +optional
	Autoscaling AutoscalingSpec `json:"autoscaling,omitempty"`

	// GuacdAutoscaling configures horizontal pod autoscaling for the guacd proxy deployment.
	// +optional
	GuacdAutoscaling AutoscalingSpec `json:"guacdAutoscaling,omitempty"`

	// MetricsExporter configures the shared Prometheus exporter deployment.
	// The deployment is created when any GuacamoleConnection has spec.exposeMetrics enabled.
	// +optional
	MetricsExporter MetricsExporterSpec `json:"metricsExporter,omitempty"`

	// OpenID configures optional OpenID Connect SSO (e.g. Keycloak) for the Guacamole web UI.
	// Omit this block entirely to use native MySQL users. When present, set enabled=true to activate SSO.
	// +optional
	OpenID *OpenIDSpec `json:"openID,omitempty"`

	// LoginBranding customizes the Guacamole login page title and logo.
	// Omit this block entirely for the default login screen, or set enabled=true to activate.
	// +optional
	LoginBranding *LoginBrandingSpec `json:"loginBranding,omitempty"`
}

// LoginBrandingSpec customizes the Guacamole web login screen via a branding extension.
// Leave spec.loginBranding unset, or set enabled=false, to keep the default Apache Guacamole login page.
// CEL rules treat empty object refs ({}) from the Console form as unset (only .name is checked).
// +kubebuilder:validation:XValidation:rule="!self.enabled || self.title != '' || self.logoSource in ['secret','configMap']",message="title and/or a logo source is required when login branding is enabled"
// +kubebuilder:validation:XValidation:rule="!self.enabled || self.logoSource != 'secret' || (has(self.logoSecretRef.name) && self.logoSecretRef.name != '')",message="logoSecretRef.name is required when logo source is Secret"
// +kubebuilder:validation:XValidation:rule="!self.enabled || self.logoSource != 'configMap' || (has(self.logoConfigMapRef.name) && self.logoConfigMapRef.name != '')",message="logoConfigMapRef.name is required when logo source is ConfigMap"
type LoginBrandingSpec struct {
	// Enabled activates custom login page branding (title and/or logo).
	// Defaults to false. When false, the default Apache Guacamole login page is used.
	// +kubebuilder:default=false
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Enable custom login branding",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:booleanSwitch"}
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Title replaces the default "Apache Guacamole" heading on the login page.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Login page title",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:loginBranding.enabled:true","urn:alm:descriptor:com.tectonic.ui:text"}
	// +optional
	Title string `json:"title,omitempty"`

	// LogoSource selects where the login logo image is stored.
	// Use "none" for title-only branding without a custom logo.
	// +kubebuilder:default=none
	// +kubebuilder:validation:Enum=none;secret;configMap
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Logo source",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:loginBranding.enabled:true","urn:alm:descriptor:com.tectonic.ui:select:none","urn:alm:descriptor:com.tectonic.ui:select:secret","urn:alm:descriptor:com.tectonic.ui:select:configMap"}
	// +optional
	LogoSource string `json:"logoSource,omitempty"`

	// LogoSecretRef provides a PNG logo from a Secret in the same namespace.
	// Required when logoSource is "secret".
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Logo (Secret)",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:loginBranding.enabled:true","urn:alm:descriptor:com.tectonic.ui:fieldDependency:loginBranding.logoSource:secret","urn:alm:descriptor:io.kubernetes:Secret"}
	// +optional
	LogoSecretRef *SecretKeyRef `json:"logoSecretRef,omitempty"`

	// LogoConfigMapRef provides a PNG logo from a ConfigMap in the same namespace.
	// Required when logoSource is "configMap".
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Logo (ConfigMap)",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:loginBranding.enabled:true","urn:alm:descriptor:com.tectonic.ui:fieldDependency:loginBranding.logoSource:configMap","urn:alm:descriptor:io.kubernetes:ConfigMap"}
	// +optional
	LogoConfigMapRef *SecretKeyRef `json:"logoConfigMapRef,omitempty"`
}

// OpenIDSpec configures Guacamole's OpenID Connect authentication extension.
// Leave spec.openID unset, or set enabled=false, to use native MySQL (JDBC) users.
// See https://guacamole.apache.org/doc/gug/openid-auth.html
// +kubebuilder:validation:XValidation:rule="!self.enabled || (self.issuer != '' && self.clientID != '')",message="issuer and clientID are required when OpenID is enabled"
type OpenIDSpec struct {
	// Enabled activates the OpenID extension for the Guacamole web UI.
	// Defaults to false. When false, users authenticate with MySQL accounts (e.g. guacadmin).
	// +kubebuilder:default=false
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Enable OpenID Connect (SSO)",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:booleanSwitch"}
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Issuer is the expected token issuer (Keycloak: https://.../realms/<realm>).
	// Required when enabled is true.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Issuer",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:openID.enabled:true","urn:alm:descriptor:com.tectonic.ui:text"}
	// +optional
	Issuer string `json:"issuer,omitempty"`

	// AuthorizationEndpoint is the OIDC authorization endpoint.
	// Defaults to <issuer>/protocol/openid-connect/auth when unset.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Authorization endpoint",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:openID.enabled:true","urn:alm:descriptor:com.tectonic.ui:advanced","urn:alm:descriptor:com.tectonic.ui:text"}
	// +optional
	AuthorizationEndpoint string `json:"authorizationEndpoint,omitempty"`

	// JWKSEndpoint is the JWKS URI used to validate ID tokens.
	// Defaults to <issuer>/protocol/openid-connect/certs when unset.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="JWKS endpoint",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:openID.enabled:true","urn:alm:descriptor:com.tectonic.ui:advanced","urn:alm:descriptor:com.tectonic.ui:text"}
	// +optional
	JWKSEndpoint string `json:"jwksEndpoint,omitempty"`

	// ClientID is the OIDC client id registered in the identity provider.
	// Required when enabled is true.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Client ID",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:openID.enabled:true","urn:alm:descriptor:com.tectonic.ui:text"}
	// +optional
	ClientID string `json:"clientID,omitempty"`

	// ClientSecretRef optionally provides a confidential client secret.
	// Not required for Guacamole's default OpenID implicit flow.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Client secret",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:openID.enabled:true","urn:alm:descriptor:com.tectonic.ui:advanced"}
	// +optional
	ClientSecretRef *SecretKeyRef `json:"clientSecretRef,omitempty"`

	// RedirectURI is the full Guacamole URL returned to after IdP login.
	// Defaults to status.routeURL when unset.
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Redirect URI",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:openID.enabled:true","urn:alm:descriptor:com.tectonic.ui:text"}
	// +optional
	RedirectURI string `json:"redirectURI,omitempty"`

	// UsernameClaimType is the JWT claim used as the Guacamole username.
	// Use preferred_username when integrating with Keycloak users that match DesktopSession subjects.
	// +kubebuilder:default="preferred_username"
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Username claim",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:openID.enabled:true","urn:alm:descriptor:com.tectonic.ui:advanced","urn:alm:descriptor:com.tectonic.ui:text"}
	// +optional
	UsernameClaimType string `json:"usernameClaimType,omitempty"`

	// Scope is the space-separated OpenID scope list.
	// +kubebuilder:default="openid email profile"
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Scope",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:openID.enabled:true","urn:alm:descriptor:com.tectonic.ui:advanced","urn:alm:descriptor:com.tectonic.ui:text"}
	// +optional
	Scope string `json:"scope,omitempty"`

	// ExtensionPriority controls login UX.
	// "*,openid" keeps the Guacamole login form and adds an SSO option.
	// "openid" redirects unauthenticated users straight to the IdP.
	// +kubebuilder:default="*,openid"
	// +operator-sdk:csv:customresourcedefinitions:type=spec,displayName="Extension priority",xDescriptors={"urn:alm:descriptor:com.tectonic.ui:fieldDependency:openID.enabled:true","urn:alm:descriptor:com.tectonic.ui:advanced","urn:alm:descriptor:com.tectonic.ui:text"}
	// +optional
	ExtensionPriority string `json:"extensionPriority,omitempty"`
}

// MetricsExporterSpec configures the shared Prometheus exporter for a Guacamole instance.
type MetricsExporterSpec struct {
	// Image is the container image for the metrics exporter.
	// +optional
	Image string `json:"image,omitempty"`

	// Port is the HTTP port where /metrics is served.
	// +kubebuilder:default=9110
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port *int32 `json:"port,omitempty"`

	// ScrapeIntervalSeconds is how often the exporter polls MySQL for active sessions.
	// +kubebuilder:default=15
	// +kubebuilder:validation:Minimum=5
	ScrapeIntervalSeconds *int32 `json:"scrapeIntervalSeconds,omitempty"`

	// Resources defines resource requirements for the metrics exporter container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// AutoscalingSpec configures an HPA for a deployment.
type AutoscalingSpec struct {
	// Enabled creates a HorizontalPodAutoscaler for the target deployment.
	// +kubebuilder:default=false
	Enabled *bool `json:"enabled,omitempty"`

	// MinReplicas is the minimum number of pods.
	// Defaults to spec.replicas or spec.guacdReplicas when unset.
	// +kubebuilder:validation:Minimum=1
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the maximum number of pods.
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=1
	MaxReplicas *int32 `json:"maxReplicas,omitempty"`

	// TargetMemoryUtilizationPercentage scales up when average memory usage
	// exceeds this percentage of the configured memory request.
	// +kubebuilder:default=80
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	TargetMemoryUtilizationPercentage *int32 `json:"targetMemoryUtilizationPercentage,omitempty"`

	// TargetCPUUtilizationPercentage optionally scales when average CPU usage
	// exceeds this percentage of the configured CPU request.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	TargetCPUUtilizationPercentage *int32 `json:"targetCPUUtilizationPercentage,omitempty"`
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

	// Path is the HTTP path exposed by the Route. Guacamole serves the web UI under /guacamole.
	// +kubebuilder:default="/guacamole"
	Path string `json:"path,omitempty"`
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

	// MetricsExporter resource requirements for the Prometheus exporter container.
	// +optional
	MetricsExporter corev1.ResourceRequirements `json:"metricsExporter,omitempty"`
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
