package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

const (
	metricsDeploySuffix  = "-metrics"
	metricsServiceSuffix = "-metrics"
)

func metricsDeployName(name string) string  { return name + metricsDeploySuffix }
func metricsServiceName(name string) string { return name + metricsServiceSuffix }

type monitoredConnection struct {
	ConnectionID   int64  `json:"connectionId,omitempty"`
	ConnectionName string `json:"connectionName"`
	RemoteHost     string `json:"remoteHost"`
}

func connectionMetricsEnabled(conn *guacamolev1alpha1.GuacamoleConnection) bool {
	if conn.Spec.ExposeMetrics == nil {
		return false
	}
	return *conn.Spec.ExposeMetrics
}

func connectionRemoteHost(conn *guacamolev1alpha1.GuacamoleConnection) string {
	switch conn.Spec.Protocol {
	case "rdp":
		if conn.Spec.RDP != nil {
			return conn.Spec.RDP.Hostname
		}
	case "vnc":
		if conn.Spec.VNC != nil {
			return conn.Spec.VNC.Hostname
		}
	case "ssh":
		if conn.Spec.SSH != nil {
			return conn.Spec.SSH.Hostname
		}
	}
	return "unknown"
}

func guacamoleRefNamespace(ref guacamolev1alpha1.GuacamoleInstanceRef, defaultNamespace string) string {
	if ref.Namespace != "" {
		return ref.Namespace
	}
	return defaultNamespace
}

func connectionTargetsGuacamole(conn *guacamolev1alpha1.GuacamoleConnection, guac *guacamolev1alpha1.Guacamole) bool {
	if conn.Spec.GuacamoleRef.Name != guac.Name {
		return false
	}
	return guacamoleRefNamespace(conn.Spec.GuacamoleRef, conn.Namespace) == guac.Namespace
}

func listMonitoredConnections(
	ctx context.Context,
	c client.Client,
	guac *guacamolev1alpha1.Guacamole,
) ([]monitoredConnection, error) {
	connList := &guacamolev1alpha1.GuacamoleConnectionList{}
	if err := c.List(ctx, connList); err != nil {
		return nil, fmt.Errorf("list GuacamoleConnections: %w", err)
	}

	monitored := make([]monitoredConnection, 0)
	for i := range connList.Items {
		conn := &connList.Items[i]
		if !connectionTargetsGuacamole(conn, guac) || !connectionMetricsEnabled(conn) {
			continue
		}
		monitored = append(monitored, monitoredConnection{
			ConnectionID:   conn.Status.ConnectionID,
			ConnectionName: connectionDisplayName(conn),
			RemoteHost:     connectionRemoteHost(conn),
		})
	}
	return monitored, nil
}

func metricsExporterPort(spec *guacamolev1alpha1.GuacamoleSpec) int32 {
	if spec.MetricsExporter.Port != nil {
		return *spec.MetricsExporter.Port
	}
	return 9110
}

func metricsScrapeInterval(spec *guacamolev1alpha1.GuacamoleSpec) int32 {
	if spec.MetricsExporter.ScrapeIntervalSeconds != nil {
		return *spec.MetricsExporter.ScrapeIntervalSeconds
	}
	return 15
}

const openshiftInternalRegistry = "image-registry.openshift-image-registry.svc:5000"

// openshiftInternalRegistryImage rewrites the external OpenShift registry route to the
// in-cluster registry service URL so pods in any namespace can pull without auth.
func openshiftInternalRegistryImage(image string) string {
	if strings.HasPrefix(image, openshiftInternalRegistry) {
		return image
	}
	slash := strings.Index(image, "/")
	if slash <= 0 {
		return image
	}
	host := image[:slash]
	if strings.Contains(host, "default-route-openshift-image-registry") ||
		strings.Contains(host, "image-registry.openshift-image-registry.svc") {
		return openshiftInternalRegistry + image[slash:]
	}
	return image
}

func metricsExporterImage(g *guacamolev1alpha1.Guacamole) string {
	var image string
	if g.Spec.MetricsExporter.Image != "" {
		image = g.Spec.MetricsExporter.Image
	} else {
		for _, key := range []string{
			"RELATED_IMAGE_GUACAMOLE_METRICS_EXPORTER",
			"METRICS_EXPORTER_IMAGE",
		} {
			if image = os.Getenv(key); image != "" {
				break
			}
		}
	}
	if image == "" {
		return ""
	}
	return openshiftInternalRegistryImage(image)
}

func defaultMetricsExporterResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("50m"),
			corev1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("200m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
	}
}

func metricsExporterResources(g *guacamolev1alpha1.Guacamole) corev1.ResourceRequirements {
	if g.Spec.MetricsExporter.Resources.Requests != nil || g.Spec.MetricsExporter.Resources.Limits != nil {
		return g.Spec.MetricsExporter.Resources
	}
	if g.Spec.Resources.MetricsExporter.Requests != nil || g.Spec.Resources.MetricsExporter.Limits != nil {
		return g.Spec.Resources.MetricsExporter
	}
	return defaultMetricsExporterResources()
}

func monitoredConnectionsJSON(monitored []monitoredConnection) (string, error) {
	payload, err := json.Marshal(monitored)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func desiredMetricsExporterDeployment(g *guacamolev1alpha1.Guacamole, monitored []monitoredConnection) (*appsv1.Deployment, error) {
	secretName := mysqlSecretName(g.Name)
	mysqlHost := serviceFQDN(mysqlServiceName(g.Name), g.Namespace)
	port := metricsExporterPort(&g.Spec)
	interval := metricsScrapeInterval(&g.Spec)
	replicas := int32Ptr(1)

	monitoredJSON, err := monitoredConnectionsJSON(monitored)
	if err != nil {
		return nil, err
	}

	image := metricsExporterImage(g)
	if image == "" {
		return nil, fmt.Errorf("metrics exporter image is not configured: set spec.metricsExporter.image on Guacamole or RELATED_IMAGE_GUACAMOLE_METRICS_EXPORTER on the operator deployment")
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      metricsDeployName(g.Name),
			Namespace: g.Namespace,
			Labels:    labelsFor(g, "metrics"),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabelsFor(g, "metrics"),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selectorLabelsFor(g, "metrics"),
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "metrics-exporter",
							Image:           image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Ports: []corev1.ContainerPort{
								{Name: "metrics", ContainerPort: port, Protocol: corev1.ProtocolTCP},
							},
							Env: append(
								guacamoleDBEnv(secretName, mysqlHost),
								corev1.EnvVar{Name: "METRICS_PORT", Value: fmt.Sprintf("%d", port)},
								corev1.EnvVar{Name: "SCRAPE_INTERVAL_SECONDS", Value: fmt.Sprintf("%d", interval)},
								corev1.EnvVar{Name: "MONITORED_CONNECTIONS", Value: monitoredJSON},
							),
							Resources: metricsExporterResources(g),
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt32(port),
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       20,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromInt32(port),
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
							},
						},
					},
				},
			},
		},
	}
	return deploy, nil
}

func desiredMetricsExporterService(g *guacamolev1alpha1.Guacamole) *corev1.Service {
	port := metricsExporterPort(&g.Spec)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      metricsServiceName(g.Name),
			Namespace: g.Namespace,
			Labels:    labelsFor(g, "metrics"),
			Annotations: map[string]string{
				"prometheus.io/scrape": "true",
				"prometheus.io/port":   fmt.Sprintf("%d", port),
				"prometheus.io/path":   "/metrics",
			},
		},
		Spec: corev1.ServiceSpec{
			Selector: selectorLabelsFor(g, "metrics"),
			Ports: []corev1.ServicePort{
				{
					Name:       "metrics",
					Port:       port,
					TargetPort: intstr.FromInt32(port),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	return svc
}

func reconcileGuacamoleMetrics(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	guac *guacamolev1alpha1.Guacamole,
) error {
	monitored, err := listMonitoredConnections(ctx, c, guac)
	if err != nil {
		return err
	}

	if len(monitored) == 0 {
		if err := deleteDeploymentIfExists(ctx, c, guac.Namespace, metricsDeployName(guac.Name)); err != nil {
			return err
		}
		return deleteServiceIfExists(ctx, c, guac.Namespace, metricsServiceName(guac.Name))
	}

	desiredDeploy, err := desiredMetricsExporterDeployment(guac, monitored)
	if err != nil {
		return err
	}
	if err := ensureMetricsImagePullAccess(ctx, c, guac.Namespace); err != nil {
		return fmt.Errorf("ensure metrics image pull access: %w", err)
	}
	if err := reconcileOwnedDeployment(ctx, c, scheme, guac, desiredDeploy); err != nil {
		return err
	}
	return reconcileOwnedService(ctx, c, scheme, guac, desiredMetricsExporterService(guac))
}

func reconcileOwnedDeployment(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	owner *guacamolev1alpha1.Guacamole,
	desired *appsv1.Deployment,
) error {
	deploy := &appsv1.Deployment{}
	deploy.Name = desired.Name
	deploy.Namespace = desired.Namespace
	_, err := controllerutil.CreateOrUpdate(ctx, c, deploy, func() error {
		if err := controllerutil.SetControllerReference(owner, deploy, scheme); err != nil {
			return err
		}
		deploy.Labels = desired.Labels
		deploy.Spec = desired.Spec
		return nil
	})
	return err
}

func reconcileOwnedService(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	owner *guacamolev1alpha1.Guacamole,
	desired *corev1.Service,
) error {
	svc := &corev1.Service{}
	svc.Name = desired.Name
	svc.Namespace = desired.Namespace
	_, err := controllerutil.CreateOrUpdate(ctx, c, svc, func() error {
		if err := controllerutil.SetControllerReference(owner, svc, scheme); err != nil {
			return err
		}
		svc.Labels = desired.Labels
		svc.Annotations = desired.Annotations
		svc.Spec.Selector = desired.Spec.Selector
		svc.Spec.Ports = desired.Spec.Ports
		svc.Spec.Type = desired.Spec.Type
		return nil
	})
	return err
}

func deleteDeploymentIfExists(ctx context.Context, c client.Client, namespace, name string) error {
	deploy := &appsv1.Deployment{}
	if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, deploy); client.IgnoreNotFound(err) != nil {
		return err
	} else if err != nil {
		return nil
	}
	return c.Delete(ctx, deploy)
}

func deleteServiceIfExists(ctx context.Context, c client.Client, namespace, name string) error {
	svc := &corev1.Service{}
	if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, svc); client.IgnoreNotFound(err) != nil {
		return err
	} else if err != nil {
		return nil
	}
	return c.Delete(ctx, svc)
}
