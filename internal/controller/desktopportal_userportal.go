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

package controller

import (
	"context"
	"fmt"
	"os"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

const userPortalHTTPPort = 8080

func userPortalEnabled(portal *guacamolev1alpha1.DesktopPortal) bool {
	if portal.Spec.UserPortal == nil {
		return false
	}
	return ptr.Deref(portal.Spec.UserPortal.Enabled, true)
}

func userPortalOIDCClientID(portal *guacamolev1alpha1.DesktopPortal) string {
	if portal.Spec.UserPortal != nil && portal.Spec.UserPortal.OIDCClientID != "" {
		return portal.Spec.UserPortal.OIDCClientID
	}
	return "guacamole-user-portal"
}

func userPortalIssuer(portal *guacamolev1alpha1.DesktopPortal) string {
	if portal.Spec.UserPortal == nil {
		return ""
	}
	return strings.TrimRight(portal.Spec.UserPortal.Issuer, "/")
}

func (r *DesktopPortalReconciler) reconcileUserPortal(
	ctx context.Context,
	portal *guacamolev1alpha1.DesktopPortal,
	names portalNames,
) (string, error) {
	if !userPortalEnabled(portal) {
		_ = r.deleteUserPortalResources(ctx, portal, names)
		return "", nil
	}
	if userPortalIssuer(portal) == "" {
		return "", fmt.Errorf("spec.userPortal.issuer is required (public Keycloak OIDC issuer URL)")
	}
	// Drop legacy OpenShift oauth-proxy leftovers from earlier UX revision.
	_ = r.deleteLegacyUserPortalOAuth(ctx, portal, names)

	if err := r.ensureUserPortalDeployment(ctx, portal, names); err != nil {
		return "", err
	}
	if err := r.ensureUserPortalService(ctx, portal, names); err != nil {
		return "", err
	}
	if err := r.ensureUserPortalRoute(ctx, portal, names); err != nil {
		return "", err
	}
	return r.lookupUserPortalURL(ctx, portal, names)
}

func (r *DesktopPortalReconciler) deleteLegacyUserPortalOAuth(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, names portalNames) error {
	_ = r.Delete(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: names.UserPortalOAuthSA, Namespace: portal.Namespace}})
	_ = r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: names.UserPortalCookieSecret, Namespace: portal.Namespace}})
	_ = r.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: names.UserPortalOAuthSA + "-auth-delegator"}})
	return nil
}

func (r *DesktopPortalReconciler) deleteUserPortalResources(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, names portalNames) error {
	objs := []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: names.UserPortalDeployment, Namespace: portal.Namespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: names.UserPortalService, Namespace: portal.Namespace}},
	}
	for _, obj := range objs {
		_ = r.Delete(ctx, obj)
	}
	_ = r.deleteLegacyUserPortalOAuth(ctx, portal, names)
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(routeGVK)
	route.SetName(names.UserPortalRoute)
	route.SetNamespace(portal.Namespace)
	_ = r.Delete(ctx, route)
	return nil
}

func (r *DesktopPortalReconciler) ensureAuthReviewClusterRBAC(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, names portalNames) error {
	cr := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: names.AuthReviewClusterRole},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cr, func() error {
		if cr.Labels == nil {
			cr.Labels = map[string]string{}
		}
		cr.Labels["app.kubernetes.io/managed-by"] = "guacamole-operator"
		cr.Labels["desktop.guacamole.io/portal"] = portal.Name
		cr.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{"authentication.k8s.io"},
				Resources: []string{"tokenreviews"},
				Verbs:     []string{"create"},
			},
			{
				APIGroups: []string{"authorization.k8s.io"},
				Resources: []string{"subjectaccessreviews"},
				Verbs:     []string{"create"},
			},
		}
		return nil
	})
	if err != nil {
		return err
	}
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: names.AuthReviewClusterRoleBinding},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, crb, func() error {
		if crb.Labels == nil {
			crb.Labels = map[string]string{}
		}
		crb.Labels["app.kubernetes.io/managed-by"] = "guacamole-operator"
		crb.Labels["desktop.guacamole.io/portal"] = portal.Name
		crb.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     names.AuthReviewClusterRole,
		}
		crb.Subjects = []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      names.APIServiceAccount,
			Namespace: portal.Namespace,
		}}
		return nil
	})
	return err
}

func (r *DesktopPortalReconciler) ensureUserPortalDeployment(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, names portalNames) error {
	uiImage := ""
	if portal.Spec.UserPortal != nil {
		uiImage = portal.Spec.UserPortal.Image
	}
	if uiImage == "" {
		uiImage = os.Getenv("RELATED_IMAGE_DESKTOP_USER_PORTAL")
	}
	if uiImage == "" {
		return fmt.Errorf("spec.userPortal.image (or RELATED_IMAGE_DESKTOP_USER_PORTAL) is required")
	}
	replicas := ptr.Deref(portal.Spec.Replicas, 1)
	apiUpstream := fmt.Sprintf("%s:%d", names.APIService, portalAPIHTTPSPort)
	issuer := userPortalIssuer(portal)
	clientID := userPortalOIDCClientID(portal)
	kcBase := issuer
	realm := portal.Spec.Keycloak.Realm
	if i := strings.LastIndex(issuer, "/realms/"); i >= 0 {
		kcBase = issuer[:i]
		realm = issuer[i+len("/realms/"):]
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: names.UserPortalDeployment, Namespace: portal.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		if err := controllerutil.SetControllerReference(portal, deploy, r.Scheme); err != nil {
			return err
		}
		deploy.Spec.Replicas = &replicas
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": names.UserPortalDeployment}}
		deploy.Spec.Template.ObjectMeta.Labels = map[string]string{"app": names.UserPortalDeployment}
		// Explicit "default" clears a previously set oauth SA (omitempty would leave the old value).
		deploy.Spec.Template.Spec.ServiceAccountName = "default"
		deploy.Spec.Template.Spec.Volumes = nil
		deploy.Spec.Template.Spec.Containers = []corev1.Container{
			{
				Name:  "user-portal",
				Image: uiImage,
				Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: userPortalHTTPPort}},
				Env: []corev1.EnvVar{
					{Name: "PORTAL_API_UPSTREAM", Value: apiUpstream},
					{Name: "OIDC_KEYCLOAK_URL", Value: kcBase},
					{Name: "OIDC_REALM", Value: realm},
					{Name: "OIDC_CLIENT_ID", Value: clientID},
					{Name: "OIDC_ISSUER", Value: issuer},
				},
				ReadinessProbe: &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
						Path: "/",
						Port: intstr.FromInt(userPortalHTTPPort),
					}},
				},
			},
		}
		return nil
	})
	return err
}

func (r *DesktopPortalReconciler) ensureUserPortalService(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, names portalNames) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: names.UserPortalService, Namespace: portal.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(portal, svc, r.Scheme); err != nil {
			return err
		}
		svc.Spec.Selector = map[string]string{"app": names.UserPortalDeployment}
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       "http",
			Port:       80,
			TargetPort: intstr.FromInt(userPortalHTTPPort),
		}}
		return nil
	})
	return err
}

func (r *DesktopPortalReconciler) ensureUserPortalRoute(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, names portalNames) error {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(routeGVK)
	route.SetName(names.UserPortalRoute)
	route.SetNamespace(portal.Namespace)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, route, func() error {
		if err := controllerutil.SetControllerReference(portal, route, r.Scheme); err != nil {
			return err
		}
		spec := map[string]interface{}{
			"to": map[string]interface{}{
				"kind":   "Service",
				"name":   names.UserPortalService,
				"weight": int64(100),
			},
			"port": map[string]interface{}{
				"targetPort": "http",
			},
			"tls": map[string]interface{}{
				"termination":                   "edge",
				"insecureEdgeTerminationPolicy": "Redirect",
			},
		}
		if portal.Spec.UserPortal != nil && portal.Spec.UserPortal.Hostname != "" {
			spec["host"] = portal.Spec.UserPortal.Hostname
		}
		return unstructured.SetNestedField(route.Object, spec, "spec")
	})
	return err
}

func (r *DesktopPortalReconciler) lookupUserPortalURL(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, names portalNames) (string, error) {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(routeGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: names.UserPortalRoute, Namespace: portal.Namespace}, route); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	host := routeHost(route)
	if host == "" {
		ingress, found, _ := unstructured.NestedSlice(route.Object, "status", "ingress")
		if found && len(ingress) > 0 {
			if m, ok := ingress[0].(map[string]interface{}); ok {
				host, _ = m["host"].(string)
			}
		}
	}
	if host == "" {
		return "", nil
	}
	return "https://" + host, nil
}
