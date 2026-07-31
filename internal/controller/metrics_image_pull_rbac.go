package controller

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var safeK8sName = regexp.MustCompile(`[^a-z0-9-]+`)

func operatorImageRegistryNamespace() string {
	for _, key := range []string{
		"RELATED_IMAGE_GUACAMOLE_METRICS_EXPORTER",
		"METRICS_EXPORTER_IMAGE",
	} {
		image := os.Getenv(key)
		if image == "" {
			continue
		}
		slash := strings.Index(image, "/")
		if slash < 0 || slash+1 >= len(image) {
			continue
		}
		rest := image[slash+1:]
		if next := strings.Index(rest, "/"); next > 0 {
			return rest[:next]
		}
	}
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns
	}
	return "guacamole-operator-system"
}

func metricsImagePullRoleBindingName(namespace string) string {
	safe := safeK8sName.ReplaceAllString(strings.ToLower(namespace), "-")
	safe = strings.Trim(safe, "-")
	if len(safe) > 40 {
		safe = safe[:40]
	}
	return fmt.Sprintf("guacamole-metrics-puller-%s", safe)
}

// ensureMetricsImagePullAccess grants service accounts in the Guacamole namespace
// permission to pull the metrics exporter image from the operator registry namespace.
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=create;get;list;patch;update;watch
func ensureMetricsImagePullAccess(ctx context.Context, c client.Client, guacamoleNamespace string) error {
	registryNamespace := operatorImageRegistryNamespace()
	if guacamoleNamespace == "" || guacamoleNamespace == registryNamespace {
		return nil
	}

	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      metricsImagePullRoleBindingName(guacamoleNamespace),
			Namespace: registryNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "guacamole-operator",
				"app.kubernetes.io/component":  "metrics-image-puller",
			},
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, c, binding, func() error {
		binding.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     "system:image-puller",
		}
		binding.Subjects = []rbacv1.Subject{
			{
				Kind:     rbacv1.GroupKind,
				APIGroup: rbacv1.GroupName,
				Name:     fmt.Sprintf("system:serviceaccounts:%s", guacamoleNamespace),
			},
		}
		return nil
	})
	return err
}
