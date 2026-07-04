package controller

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

const (
	mysqlSecretSuffix  = "-mysql"
	mysqlPVCSuffix     = "-mysql"
	mysqlDeploySuffix  = "-mysql"
	mysqlServiceSuffix = "-mysql"
	guacdDeploySuffix  = "-guacd"
	guacdServiceSuffix = "-guacd"
	guacDeploySuffix   = "-guacamole"
	guacServiceSuffix  = "-guacamole"
	routeSuffix        = "-guacamole"
)

func mysqlSecretName(name string) string  { return name + mysqlSecretSuffix }
func mysqlPVCName(name string) string     { return name + mysqlPVCSuffix }
func mysqlDeployName(name string) string  { return name + mysqlDeploySuffix }
func mysqlServiceName(name string) string { return name + mysqlServiceSuffix }
func guacdDeployName(name string) string  { return name + guacdDeploySuffix }
func guacdServiceName(name string) string { return name + guacdServiceSuffix }
func guacDeployName(name string) string   { return name + guacDeploySuffix }
func guacServiceName(name string) string  { return name + guacServiceSuffix }
func routeName(name string) string        { return name + routeSuffix }

func serviceFQDN(service, namespace string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", service, namespace)
}

func int32Ptr(v int32) *int32 { return &v }
func boolPtr(v bool) *bool    { return &v }

func defaultReplicas(spec *guacamolev1alpha1.GuacamoleSpec) int32 {
	if spec.Replicas != nil {
		return *spec.Replicas
	}
	return 1
}

func defaultGuacdReplicas(spec *guacamolev1alpha1.GuacamoleSpec) int32 {
	if spec.GuacdReplicas != nil {
		return *spec.GuacdReplicas
	}
	return 1
}

func routeEnabled(spec *guacamolev1alpha1.GuacamoleSpec) bool {
	if spec.Route.Enabled == nil {
		return true
	}
	return *spec.Route.Enabled
}

func defaultGuacamoleResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
}

func defaultGuacdResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
}

func defaultMySQLResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
}

func mysqlEnvFromSecret(secretName string) []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name: "MYSQL_USER",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
					Key:                  "database-user",
				},
			},
		},
		{
			Name: "MYSQL_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
					Key:                  "database-password",
				},
			},
		},
		{
			Name: "MYSQL_ROOT_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
					Key:                  "database-root-password",
				},
			},
		},
		{
			Name: "MYSQL_DATABASE",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
					Key:                  "database-name",
				},
			},
		},
	}
}

func guacamoleDBEnv(secretName, mysqlHost string) []corev1.EnvVar {
	env := mysqlEnvFromSecret(secretName)
	env = append(env,
		corev1.EnvVar{Name: "MYSQL_HOSTNAME", Value: mysqlHost},
		corev1.EnvVar{Name: "MYSQL_PORT", Value: "3306"},
	)
	return env
}

func desiredMySQLSecret(g *guacamolev1alpha1.Guacamole) *corev1.Secret {
	db := g.Spec.Database
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysqlSecretName(g.Name),
			Namespace: g.Namespace,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"database-user":          valueOrDefault(db.User, "guacamole_user"),
			"database-password":      valueOrDefault(db.Password, "guacamole_pass"),
			"database-root-password": valueOrDefault(db.RootPassword, "rootpass123"),
			"database-name":          valueOrDefault(db.Name, "guacamole_db"),
		},
	}
	return secret
}

func desiredMySQLPVC(g *guacamolev1alpha1.Guacamole) *corev1.PersistentVolumeClaim {
	storageSize := valueOrDefault(g.Spec.Database.StorageSize, "5Gi")
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysqlPVCName(g.Name),
			Namespace: g.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse(storageSize),
				},
			},
		},
	}
	if g.Spec.Database.StorageClassName != "" {
		pvc.Spec.StorageClassName = &g.Spec.Database.StorageClassName
	}
	return pvc
}

func desiredMySQLDeployment(g *guacamolev1alpha1.Guacamole) *appsv1.Deployment {
	secretName := mysqlSecretName(g.Name)
	mysqlImage := valueOrDefault(g.Spec.MySQLImage, "mysql:8.0")
	mysqlResources := g.Spec.Resources.MySQL
	if mysqlResources.Requests == nil && mysqlResources.Limits == nil {
		mysqlResources = defaultMySQLResources()
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysqlDeployName(g.Name),
			Namespace: g.Namespace,
			Labels:    labelsFor(g, "mysql"),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(1),
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabelsFor(g, "mysql"),
			},
			Strategy: appsv1.DeploymentStrategy{
				Type: appsv1.RecreateDeploymentStrategyType,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selectorLabelsFor(g, "mysql"),
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "mysql-data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: mysqlPVCName(g.Name),
								},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "mysql",
							Image:           mysqlImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Ports:           []corev1.ContainerPort{{ContainerPort: 3306, Protocol: corev1.ProtocolTCP}},
							Env:             mysqlEnvFromSecret(secretName),
							Resources:       mysqlResources,
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"/bin/sh", "-i", "-c",
											`MYSQL_PWD="$MYSQL_PASSWORD" mysqladmin -u $MYSQL_USER ping`,
										},
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
								TimeoutSeconds:      1,
								FailureThreshold:    3,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									Exec: &corev1.ExecAction{
										Command: []string{
											"/bin/sh", "-i", "-c",
											`MYSQL_PWD="$MYSQL_PASSWORD" mysqladmin -u $MYSQL_USER ping`,
										},
									},
								},
								InitialDelaySeconds: 5,
								PeriodSeconds:       10,
								TimeoutSeconds:      1,
								FailureThreshold:    3,
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "mysql-data", MountPath: "/var/lib/mysql"},
							},
						},
					},
				},
			},
		},
	}
	return deploy
}

func desiredMySQLService(g *guacamolev1alpha1.Guacamole) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mysqlServiceName(g.Name),
			Namespace: g.Namespace,
			Labels:    labelsFor(g, "mysql"),
		},
		Spec: corev1.ServiceSpec{
			Selector: selectorLabelsFor(g, "mysql"),
			Ports: []corev1.ServicePort{
				{
					Name:       "mysql",
					Port:       3306,
					TargetPort: intstr.FromInt32(3306),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	return svc
}

func desiredGuacdDeployment(g *guacamolev1alpha1.Guacamole) *appsv1.Deployment {
	guacdImage := valueOrDefault(g.Spec.GuacdImage, "guacamole/guacd:1.6.0")
	logLevel := valueOrDefault(g.Spec.LogLevel, "info")
	guacdResources := g.Spec.Resources.Guacd
	if guacdResources.Requests == nil && guacdResources.Limits == nil {
		guacdResources = defaultGuacdResources()
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      guacdDeployName(g.Name),
			Namespace: g.Namespace,
			Labels:    labelsFor(g, "guacd"),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(defaultGuacdReplicas(&g.Spec)),
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabelsFor(g, "guacd"),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selectorLabelsFor(g, "guacd"),
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "freerdp-config",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "guacd",
							Image:           guacdImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Ports:           []corev1.ContainerPort{{ContainerPort: 4822, Protocol: corev1.ProtocolTCP}},
							Env: []corev1.EnvVar{
								{Name: "HOME", Value: "/home/guac"},
								{Name: "GUACD_HOSTNAME", Value: "127.0.0.1"},
								{Name: "GUACD_PORT", Value: "4822"},
								{Name: "LOG_LEVEL", Value: logLevel},
							},
							Resources: guacdResources,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "freerdp-config", MountPath: "/home/guac/.config/freerdp"},
							},
						},
					},
				},
			},
		},
	}
	return deploy
}

func desiredGuacdService(g *guacamolev1alpha1.Guacamole) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      guacdServiceName(g.Name),
			Namespace: g.Namespace,
			Labels:    labelsFor(g, "guacd"),
		},
		Spec: corev1.ServiceSpec{
			Selector: selectorLabelsFor(g, "guacd"),
			Ports: []corev1.ServicePort{
				{
					Name:       "guacd",
					Port:       4822,
					TargetPort: intstr.FromInt32(4822),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	return svc
}

func desiredGuacamoleDeployment(g *guacamolev1alpha1.Guacamole) *appsv1.Deployment {
	secretName := mysqlSecretName(g.Name)
	mysqlHost := serviceFQDN(mysqlServiceName(g.Name), g.Namespace)
	guacdHost := serviceFQDN(guacdServiceName(g.Name), g.Namespace)
	guacamoleImage := valueOrDefault(g.Spec.GuacamoleImage, "guacamole/guacamole:1.6.0")
	logLevel := valueOrDefault(g.Spec.LogLevel, "info")
	guacResources := g.Spec.Resources.Guacamole
	if guacResources.Requests == nil && guacResources.Limits == nil {
		guacResources = defaultGuacamoleResources()
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      guacDeployName(g.Name),
			Namespace: g.Namespace,
			Labels:    labelsFor(g, "guacamole"),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(defaultReplicas(&g.Spec)),
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabelsFor(g, "guacamole"),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: selectorLabelsFor(g, "guacamole"),
				},
				Spec: corev1.PodSpec{
					Volumes: []corev1.Volume{
						{
							Name: "initdb",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
					},
					InitContainers: []corev1.Container{
						{
							Name:            "initdb",
							Image:           guacamoleImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"/bin/sh", "-c"},
							Args:            []string{"set -e; /opt/guacamole/bin/initdb.sh --mysql > /initdb/initdb.sql; test -s /initdb/initdb.sql"},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "initdb", MountPath: "/initdb"},
							},
						},
						{
							Name:            "apply-initdb",
							Image:           valueOrDefault(g.Spec.MySQLImage, "mysql:8.0"),
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"/bin/sh", "-c"},
							Args: []string{`
set -e

echo "Waiting for MySQL to be ready..."
attempt=0
max_attempts=60
until mysql -h "$MYSQL_HOSTNAME" -u "$MYSQL_USER" -p"$MYSQL_PASSWORD" -e "SELECT 1" >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge "$max_attempts" ]; then
    echo "MySQL did not become ready in time."
    exit 1
  fi
  echo "MySQL not ready (attempt $attempt/$max_attempts), waiting..."
  sleep 5
done

echo "Checking if Guacamole DB schema is already initialized..."
if mysql -h "$MYSQL_HOSTNAME" -u "$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE" \
  -e "SELECT 1 FROM guacamole_user LIMIT 1;" >/dev/null 2>&1; then
  echo "Guacamole schema already present, skipping initialization."
else
  echo "Initializing Guacamole schema..."
  mysql -h "$MYSQL_HOSTNAME" -u "$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE" < /initdb/initdb.sql
  mysql -h "$MYSQL_HOSTNAME" -u "$MYSQL_USER" -p"$MYSQL_PASSWORD" "$MYSQL_DATABASE" \
    -e "SELECT 1 FROM guacamole_user LIMIT 1;" >/dev/null
  echo "Guacamole schema initialization complete."
fi
`},
							Env: guacamoleDBEnv(secretName, mysqlHost),
							VolumeMounts: []corev1.VolumeMount{
								{Name: "initdb", MountPath: "/initdb"},
							},
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "guacamole",
							Image:           guacamoleImage,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Ports:           []corev1.ContainerPort{{ContainerPort: 8080, Protocol: corev1.ProtocolTCP}},
							Env: append(
								guacamoleDBEnv(secretName, mysqlHost),
								corev1.EnvVar{Name: "GUACAMOLE_HOME", Value: "/tmp"},
								corev1.EnvVar{Name: "GUACD_HOSTNAME", Value: guacdHost},
								corev1.EnvVar{Name: "GUACD_PORT", Value: "4822"},
								corev1.EnvVar{Name: "LOG_LEVEL", Value: logLevel},
							),
							Resources: guacResources,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "initdb", MountPath: "/initdb"},
							},
						},
					},
				},
			},
		},
	}
	return deploy
}

func desiredGuacamoleService(g *guacamolev1alpha1.Guacamole) *corev1.Service {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      guacServiceName(g.Name),
			Namespace: g.Namespace,
			Labels:    labelsFor(g, "guacamole"),
		},
		Spec: corev1.ServiceSpec{
			Selector: selectorLabelsFor(g, "guacamole"),
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       8080,
					TargetPort: intstr.FromInt32(8080),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
	return svc
}

func labelsFor(g *guacamolev1alpha1.Guacamole, component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "guacamole-operator",
		"app.kubernetes.io/instance":   g.Name,
		"app.kubernetes.io/component":  component,
		"app.kubernetes.io/managed-by": "guacamole-operator",
	}
}

func selectorLabelsFor(g *guacamolev1alpha1.Guacamole, component string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":      "guacamole-operator",
		"app.kubernetes.io/instance":  g.Name,
		"app.kubernetes.io/component": component,
	}
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
