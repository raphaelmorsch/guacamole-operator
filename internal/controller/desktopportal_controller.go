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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

const (
	desktopPortalFinalizer = "guacamole.guacamole.io/desktopportal-finalizer"
	portalPluginName       = "guacamole-desktop-portal"
	portalAPIPort          = 8080
	portalAPIHTTPSPort     = 8443
	portalPluginPort       = 9443
)

var consolePluginGVK = schema.GroupVersionKind{
	Group:   "console.openshift.io",
	Version: "v1",
	Kind:    "ConsolePlugin",
}

var consoleOperatorGVK = schema.GroupVersionKind{
	Group:   "operator.openshift.io",
	Version: "v1",
	Kind:    "Console",
}

// DesktopPortalReconciler deploys the Console dynamic plugin + portal API.
type DesktopPortalReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktopportals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktopportals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktopportals/finalizers,verbs=update
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktopsessions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktoppools,verbs=get;list;watch
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=guacamoles,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;serviceaccounts;secrets;configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=console.openshift.io,resources=consoleplugins,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.openshift.io,resources=consoles,verbs=get;list;watch;patch;update
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=clusterroles;clusterrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=guacamoleconnections,verbs=get;list;watch

func (r *DesktopPortalReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	portal := &guacamolev1alpha1.DesktopPortal{}
	if err := r.Get(ctx, req.NamespacedName, portal); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !portal.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, portal)
	}

	if !controllerutil.ContainsFinalizer(portal, desktopPortalFinalizer) {
		controllerutil.AddFinalizer(portal, desktopPortalFinalizer)
		if err := r.Update(ctx, portal); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if err := r.validate(portal); err != nil {
		return r.fail(ctx, portal, err.Error())
	}

	secretName := portal.Spec.Keycloak.ClientSecretRef.Name
	if secretName == "" {
		return r.fail(ctx, portal, "spec.keycloak.clientSecretRef.name is required")
	}
	secretKey := portal.Spec.Keycloak.ClientSecretRef.Key
	if secretKey == "" {
		secretKey = "password"
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: portal.Namespace}, secret); err != nil {
		return r.fail(ctx, portal, fmt.Sprintf("keycloak secret: %v", err))
	}
	if _, ok := secret.Data[secretKey]; !ok {
		return r.fail(ctx, portal, fmt.Sprintf("keycloak secret %q missing key %q", secretName, secretKey))
	}

	sessionNS := portal.Spec.SessionNamespace
	if sessionNS == "" {
		sessionNS = portal.Spec.DefaultPool.Namespace
	}
	if sessionNS == "" {
		sessionNS = portal.Namespace
	}
	poolNS := portal.Spec.DefaultPool.Namespace
	if poolNS == "" {
		poolNS = sessionNS
	}

	names := portalResourceNames(portal)

	if err := r.ensureServiceAccount(ctx, portal, names.APIServiceAccount); err != nil {
		return r.requeueOrFail(ctx, portal, err)
	}
	if err := r.ensureSessionRBAC(ctx, portal, names.APIServiceAccount, sessionNS); err != nil {
		return r.requeueOrFail(ctx, portal, err)
	}
	if err := r.ensureGuacamoleRBACFromPool(ctx, portal, names.APIServiceAccount, portal.Spec.DefaultPool.Name, poolNS); err != nil {
		return r.requeueOrFail(ctx, portal, err)
	}
	// Console admin APIs authenticate via TokenReview/SAR.
	if err := r.ensureAuthReviewClusterRBAC(ctx, portal, names); err != nil {
		return r.requeueOrFail(ctx, portal, err)
	}
	if err := r.ensureAPIDeployment(ctx, portal, names, sessionNS, poolNS, secretName, secretKey); err != nil {
		return r.requeueOrFail(ctx, portal, err)
	}
	if err := r.ensureAPIService(ctx, portal, names); err != nil {
		return r.requeueOrFail(ctx, portal, err)
	}
	if err := r.ensurePluginDeployment(ctx, portal, names); err != nil {
		return r.requeueOrFail(ctx, portal, err)
	}
	if err := r.ensurePluginService(ctx, portal, names); err != nil {
		return r.requeueOrFail(ctx, portal, err)
	}
	if err := r.ensureConsolePlugin(ctx, portal, names); err != nil {
		return r.requeueOrFail(ctx, portal, err)
	}
	if ptr.Deref(portal.Spec.EnablePlugin, true) {
		if err := r.ensureConsoleEnabled(ctx, names.Plugin); err != nil {
			logger.Info("could not auto-enable console plugin; enable manually", "error", err.Error())
		}
	}
	userPortalURL, err := r.reconcileUserPortal(ctx, portal, names)
	if err != nil {
		return r.requeueOrFail(ctx, portal, err)
	}

	portal.Status.Phase = guacamolev1alpha1.DesktopPortalPhaseReady
	portal.Status.PluginName = names.Plugin
	portal.Status.PluginService = names.PluginService
	portal.Status.APIService = names.APIService
	portal.Status.ConsolePath = "/guacamole-desktops"
	portal.Status.UserPortalURL = userPortalURL
	meta.SetStatusCondition(&portal.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionTrue,
		Reason:             "Deployed",
		Message:            "Console plugin and portal API are reconciled",
		ObservedGeneration: portal.Generation,
	})
	if err := r.Status().Update(ctx, portal); err != nil {
		return ctrl.Result{}, err
	}
	if userPortalEnabled(portal) && userPortalURL == "" {
		return ctrl.Result{Requeue: true}, nil
	}
	return ctrl.Result{}, nil
}

type portalNames struct {
	Plugin                      string
	PluginService               string
	PluginDeployment            string
	APIService                  string
	APIDeployment               string
	APIServiceAccount           string
	SessionRole                 string
	SessionRoleBinding          string
	GuacamoleRole               string
	GuacamoleRoleBinding        string
	UserPortalDeployment        string
	UserPortalService           string
	UserPortalRoute             string
	UserPortalOAuthSA           string
	UserPortalCookieSecret      string
	AuthReviewClusterRole       string
	AuthReviewClusterRoleBinding string
}

func portalResourceNames(portal *guacamolev1alpha1.DesktopPortal) portalNames {
	base := fmt.Sprintf("%s-portal", portal.Name)
	return portalNames{
		Plugin:                       portalPluginName,
		PluginService:                base + "-plugin",
		PluginDeployment:             base + "-plugin",
		APIService:                   base + "-api",
		APIDeployment:                base + "-api",
		APIServiceAccount:            base + "-api",
		SessionRole:                  base + "-sessions",
		SessionRoleBinding:           base + "-sessions",
		GuacamoleRole:                base + "-guacamole",
		GuacamoleRoleBinding:         base + "-guacamole",
		UserPortalDeployment:         base + "-user",
		UserPortalService:            base + "-user",
		UserPortalRoute:              base + "-user",
		UserPortalOAuthSA:            base + "-user-oauth",
		UserPortalCookieSecret:       base + "-user-proxy",
		AuthReviewClusterRole:        base + "-authreview",
		AuthReviewClusterRoleBinding: base + "-authreview",
	}
}

func (r *DesktopPortalReconciler) validate(portal *guacamolev1alpha1.DesktopPortal) error {
	if portal.Spec.DefaultPool.Name == "" {
		return fmt.Errorf("spec.defaultPool.name is required")
	}
	if portal.Spec.Keycloak.URL == "" || portal.Spec.Keycloak.Realm == "" || portal.Spec.Keycloak.ClientID == "" {
		return fmt.Errorf("spec.keycloak.url, realm and clientID are required")
	}
	return nil
}

func (r *DesktopPortalReconciler) fail(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, msg string) (ctrl.Result, error) {
	portal.Status.Phase = guacamolev1alpha1.DesktopPortalPhaseFailed
	meta.SetStatusCondition(&portal.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             "Error",
		Message:            msg,
		ObservedGeneration: portal.Generation,
	})
	_ = r.Status().Update(ctx, portal)
	return ctrl.Result{}, fmt.Errorf("%s", msg)
}

// requeueOrFail retries conflicts/transient API errors without marking the CR Failed.
func (r *DesktopPortalReconciler) requeueOrFail(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, err error) (ctrl.Result, error) {
	if apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) {
		return ctrl.Result{Requeue: true}, err
	}
	return r.fail(ctx, portal, err.Error())
}

func (r *DesktopPortalReconciler) finalize(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal) (ctrl.Result, error) {
	names := portalResourceNames(portal)
	_ = r.deleteUserPortalResources(ctx, portal, names)
	_ = r.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: names.AuthReviewClusterRoleBinding}})
	_ = r.Delete(ctx, &rbacv1.ClusterRole{ObjectMeta: metav1.ObjectMeta{Name: names.AuthReviewClusterRole}})
	_ = r.Delete(ctx, &rbacv1.ClusterRoleBinding{ObjectMeta: metav1.ObjectMeta{Name: names.UserPortalOAuthSA + "-auth-delegator"}})
	if ptr.Deref(portal.Spec.EnablePlugin, true) {
		_ = r.disableConsolePlugin(ctx, names.Plugin)
	}
	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(consolePluginGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: names.Plugin}, cp); err == nil {
		_ = r.Delete(ctx, cp)
	}

	sessionNS := portal.Spec.SessionNamespace
	if sessionNS == "" {
		sessionNS = portal.Spec.DefaultPool.Namespace
	}
	if sessionNS == "" {
		sessionNS = portal.Namespace
	}
	if sessionNS != portal.Namespace {
		rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: names.SessionRoleBinding, Namespace: sessionNS}}
		_ = r.Delete(ctx, rb)
		role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: names.SessionRole, Namespace: sessionNS}}
		_ = r.Delete(ctx, role)
	}
	poolNS := portal.Spec.DefaultPool.Namespace
	if poolNS == "" {
		poolNS = sessionNS
	}
	_ = r.cleanupGuacamoleRBACFromPool(ctx, portal, portal.Spec.DefaultPool.Name, poolNS)

	controllerutil.RemoveFinalizer(portal, desktopPortalFinalizer)
	if err := r.Update(ctx, portal); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *DesktopPortalReconciler) ensureServiceAccount(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, name string) error {
	sa := &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: portal.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		return controllerutil.SetControllerReference(portal, sa, r.Scheme)
	})
	return err
}

func (r *DesktopPortalReconciler) ensureGuacamoleRBACFromPool(
	ctx context.Context,
	portal *guacamolev1alpha1.DesktopPortal,
	saName, poolName, poolNS string,
) error {
	if poolName == "" {
		return nil
	}
	pool := &guacamolev1alpha1.DesktopPool{}
	if err := r.Get(ctx, types.NamespacedName{Name: poolName, Namespace: poolNS}, pool); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	guacName := pool.Spec.Guacamole.InstanceRef.Name
	if guacName == "" {
		return nil
	}
	guacNS := pool.Spec.Guacamole.InstanceRef.Namespace
	if guacNS == "" {
		guacNS = pool.Namespace
	}
	return r.ensureGuacamoleRBAC(ctx, portal, saName, guacNS)
}

func (r *DesktopPortalReconciler) cleanupGuacamoleRBACFromPool(
	ctx context.Context,
	portal *guacamolev1alpha1.DesktopPortal,
	poolName, poolNS string,
) error {
	if poolName == "" {
		return nil
	}
	pool := &guacamolev1alpha1.DesktopPool{}
	if err := r.Get(ctx, types.NamespacedName{Name: poolName, Namespace: poolNS}, pool); err != nil {
		return nil
	}
	guacNS := pool.Spec.Guacamole.InstanceRef.Namespace
	if guacNS == "" {
		guacNS = pool.Namespace
	}
	if guacNS == portal.Namespace {
		return nil
	}
	names := portalResourceNames(portal)
	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: names.GuacamoleRoleBinding, Namespace: guacNS}}
	_ = r.Delete(ctx, rb)
	role := &rbacv1.Role{ObjectMeta: metav1.ObjectMeta{Name: names.GuacamoleRole, Namespace: guacNS}}
	_ = r.Delete(ctx, role)
	return nil
}

func (r *DesktopPortalReconciler) ensureGuacamoleRBAC(
	ctx context.Context,
	portal *guacamolev1alpha1.DesktopPortal,
	saName, guacamoleNS string,
) error {
	names := portalResourceNames(portal)
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: names.GuacamoleRole, Namespace: guacamoleNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		if guacamoleNS == portal.Namespace {
			if err := controllerutil.SetControllerReference(portal, role, r.Scheme); err != nil {
				return err
			}
		}
		role.Rules = []rbacv1.PolicyRule{{
			APIGroups: []string{"guacamole.guacamole.io"},
			Resources: []string{"guacamoles"},
			Verbs:     []string{"get", "list", "watch", "patch", "update"},
		}}
		return nil
	})
	if err != nil {
		return err
	}

	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: names.GuacamoleRoleBinding, Namespace: guacamoleNS},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		if guacamoleNS == portal.Namespace {
			if err := controllerutil.SetControllerReference(portal, rb, r.Scheme); err != nil {
				return err
			}
		}
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     names.GuacamoleRole,
		}
		rb.Subjects = []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      saName,
			Namespace: portal.Namespace,
		}}
		return nil
	})
	return err
}

func (r *DesktopPortalReconciler) ensureSessionRBAC(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, saName, sessionNS string) error {
	names := portalResourceNames(portal)
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: names.SessionRole, Namespace: sessionNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		// Role may be in another namespace; only set ownerRef when same namespace.
		if sessionNS == portal.Namespace {
			if err := controllerutil.SetControllerReference(portal, role, r.Scheme); err != nil {
				return err
			}
		}
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{"guacamole.guacamole.io"},
				Resources: []string{"desktopsessions"},
				Verbs:     []string{"get", "list", "watch", "create", "update", "patch", "delete"},
			},
			{
				APIGroups: []string{"guacamole.guacamole.io"},
				Resources: []string{"desktoppools"},
				Verbs:     []string{"get", "list", "watch", "patch", "update"},
			},
			{
				APIGroups: []string{"guacamole.guacamole.io"},
				Resources: []string{"guacamoleconnections"},
				Verbs:     []string{"get", "list", "watch"},
			},
		}
		return nil
	})
	if err != nil {
		return err
	}

	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: names.SessionRoleBinding, Namespace: sessionNS},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		if sessionNS == portal.Namespace {
			if err := controllerutil.SetControllerReference(portal, rb, r.Scheme); err != nil {
				return err
			}
		}
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     names.SessionRole,
		}
		rb.Subjects = []rbacv1.Subject{{
			Kind:      "ServiceAccount",
			Name:      saName,
			Namespace: portal.Namespace,
		}}
		return nil
	})
	return err
}

func (r *DesktopPortalReconciler) ensureAPIDeployment(
	ctx context.Context,
	portal *guacamolev1alpha1.DesktopPortal,
	names portalNames,
	sessionNS, poolNS, secretName, secretKey string,
) error {
	image := portal.Spec.APIImage
	if image == "" {
		image = os.Getenv("RELATED_IMAGE_DESKTOP_PORTAL_API")
	}
	if image == "" {
		return fmt.Errorf("spec.apiImage (or RELATED_IMAGE_DESKTOP_PORTAL_API) is required")
	}
	replicas := ptr.Deref(portal.Spec.Replicas, 1)
	display := portal.Spec.DisplayName
	if display == "" {
		display = "Desktop Sessions"
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: names.APIDeployment, Namespace: portal.Namespace},
	}
	mutate := func() error {
		if err := controllerutil.SetControllerReference(portal, deploy, r.Scheme); err != nil {
			return err
		}
		deploy.Spec.Replicas = &replicas
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": names.APIDeployment}}
		deploy.Spec.Template.ObjectMeta.Labels = map[string]string{"app": names.APIDeployment}
		deploy.Spec.Template.Spec.ServiceAccountName = names.APIServiceAccount
		deploy.Spec.Template.Spec.Volumes = []corev1.Volume{{
			Name: "serving-cert",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: names.APIService + "-cert",
					Optional:   ptr.To(true),
				},
			},
		}}
		deploy.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  "portal-api",
			Image: image,
			Ports: []corev1.ContainerPort{
				{Name: "http", ContainerPort: portalAPIPort},
				{Name: "https", ContainerPort: portalAPIHTTPSPort},
			},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "serving-cert", MountPath: "/var/serving-cert"},
			},
			Env: []corev1.EnvVar{
				{Name: "LISTEN_ADDR", Value: fmt.Sprintf(":%d", portalAPIPort)},
				{Name: "TLS_LISTEN_ADDR", Value: fmt.Sprintf(":%d", portalAPIHTTPSPort)},
				{Name: "TLS_CERT_FILE", Value: "/var/serving-cert/tls.crt"},
				{Name: "TLS_KEY_FILE", Value: "/var/serving-cert/tls.key"},
				{Name: "DISPLAY_NAME", Value: display},
				{Name: "SESSION_NAMESPACE", Value: sessionNS},
				{Name: "POOL_NAME", Value: portal.Spec.DefaultPool.Name},
				{Name: "POOL_NAMESPACE", Value: poolNS},
				{Name: "PLUGIN_NAME", Value: names.Plugin},
				{Name: "KEYCLOAK_URL", Value: portal.Spec.Keycloak.URL},
				{Name: "KEYCLOAK_REALM", Value: portal.Spec.Keycloak.Realm},
				{Name: "KEYCLOAK_CLIENT_ID", Value: portal.Spec.Keycloak.ClientID},
				{
					Name: "KEYCLOAK_CLIENT_SECRET",
					ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
						Key:                  secretKey,
					}},
				},
				{Name: "KEYCLOAK_INSECURE_SKIP_VERIFY", Value: fmt.Sprintf("%t", portal.Spec.Keycloak.InsecureSkipVerify)},
				{Name: "ADMIN_GROUPS", Value: strings.Join(portal.Spec.AdminGroups, ",")},
				{Name: "OIDC_ISSUER", Value: userPortalIssuer(portal)},
				{Name: "OIDC_CLIENT_ID", Value: userPortalOIDCClientID(portal)},
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt(portalAPIHTTPSPort),
					Scheme: corev1.URISchemeHTTPS,
				}},
			},
			LivenessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt(portalAPIHTTPSPort),
					Scheme: corev1.URISchemeHTTPS,
				}},
			},
		}}
		return nil
	}
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		_, err = controllerutil.CreateOrUpdate(ctx, r.Client, deploy, mutate)
		if err == nil || !apierrors.IsConflict(err) {
			return err
		}
		// Reload and retry on conflict (Deployment watch often races with CreateOrUpdate).
		if getErr := r.Get(ctx, types.NamespacedName{Name: names.APIDeployment, Namespace: portal.Namespace}, deploy); getErr != nil && !apierrors.IsNotFound(getErr) {
			return getErr
		}
	}
	return err
}

func (r *DesktopPortalReconciler) ensureAPIService(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, names portalNames) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: names.APIService, Namespace: portal.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(portal, svc, r.Scheme); err != nil {
			return err
		}
		if svc.Annotations == nil {
			svc.Annotations = map[string]string{}
		}
		svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"] = names.APIService + "-cert"
		svc.Spec.Selector = map[string]string{"app": names.APIDeployment}
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       "https",
			Port:       portalAPIHTTPSPort,
			TargetPort: intstr.FromInt(portalAPIHTTPSPort),
		}}
		return nil
	})
	return err
}

func (r *DesktopPortalReconciler) ensurePluginDeployment(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, names portalNames) error {
	image := portal.Spec.PluginImage
	if image == "" {
		image = os.Getenv("RELATED_IMAGE_DESKTOP_PORTAL_PLUGIN")
	}
	if image == "" {
		return fmt.Errorf("spec.pluginImage (or RELATED_IMAGE_DESKTOP_PORTAL_PLUGIN) is required")
	}
	replicas := ptr.Deref(portal.Spec.Replicas, 1)
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: names.PluginDeployment, Namespace: portal.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		if err := controllerutil.SetControllerReference(portal, deploy, r.Scheme); err != nil {
			return err
		}
		deploy.Spec.Replicas = &replicas
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: map[string]string{"app": names.PluginDeployment}}
		deploy.Spec.Template.ObjectMeta.Labels = map[string]string{"app": names.PluginDeployment}
		deploy.Spec.Template.Spec.Volumes = []corev1.Volume{
			{
				Name: "serving-cert",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: names.PluginService + "-cert",
						Optional:   ptr.To(true),
					},
				},
			},
		}
		deploy.Spec.Template.Spec.Containers = []corev1.Container{{
			Name:  "plugin",
			Image: image,
			Ports: []corev1.ContainerPort{{Name: "https", ContainerPort: portalPluginPort}},
			VolumeMounts: []corev1.VolumeMount{
				{Name: "serving-cert", MountPath: "/var/serving-cert"},
			},
			ReadinessProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
					Path:   "/plugin-manifest.json",
					Port:   intstr.FromInt(portalPluginPort),
					Scheme: corev1.URISchemeHTTPS,
				}},
			},
		}}
		return nil
	})
	return err
}

func (r *DesktopPortalReconciler) ensurePluginService(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, names portalNames) error {
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: names.PluginService, Namespace: portal.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(portal, svc, r.Scheme); err != nil {
			return err
		}
		if svc.Annotations == nil {
			svc.Annotations = map[string]string{}
		}
		svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"] = names.PluginService + "-cert"
		svc.Spec.Selector = map[string]string{"app": names.PluginDeployment}
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       "https",
			Port:       portalPluginPort,
			TargetPort: intstr.FromInt(portalPluginPort),
		}}
		return nil
	})
	return err
}

func (r *DesktopPortalReconciler) ensureConsolePlugin(ctx context.Context, portal *guacamolev1alpha1.DesktopPortal, names portalNames) error {
	display := portal.Spec.DisplayName
	if display == "" {
		display = "Desktop Sessions"
	}
	cp := &unstructured.Unstructured{}
	cp.SetGroupVersionKind(consolePluginGVK)
	cp.SetName(names.Plugin)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cp, func() error {
		_ = unstructured.SetNestedField(cp.Object, display, "spec", "displayName")
		_ = unstructured.SetNestedMap(cp.Object, map[string]interface{}{
			"type": "Service",
			"service": map[string]interface{}{
				"name":      names.PluginService,
				"namespace": portal.Namespace,
				"port":      int64(portalPluginPort),
				"basePath":  "/",
			},
		}, "spec", "backend")
		_ = unstructured.SetNestedSlice(cp.Object, []interface{}{
			map[string]interface{}{
				"alias":         "portal-api",
				"authorization": "UserToken",
				"endpoint": map[string]interface{}{
					"type": "Service",
					"service": map[string]interface{}{
						"name":      names.APIService,
						"namespace": portal.Namespace,
						"port":      int64(portalAPIHTTPSPort),
					},
				},
			},
		}, "spec", "proxy")
		return nil
	})
	return err
}

func (r *DesktopPortalReconciler) ensureConsoleEnabled(ctx context.Context, pluginName string) error {
	console := &unstructured.Unstructured{}
	console.SetGroupVersionKind(consoleOperatorGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
		return err
	}
	plugins, found, err := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
	if err != nil {
		return err
	}
	if found {
		for _, p := range plugins {
			if p == pluginName {
				return nil
			}
		}
		plugins = append(plugins, pluginName)
	} else {
		plugins = []string{pluginName}
	}
	_ = unstructured.SetNestedStringSlice(console.Object, plugins, "spec", "plugins")
	return r.Update(ctx, console)
}

func (r *DesktopPortalReconciler) disableConsolePlugin(ctx context.Context, pluginName string) error {
	console := &unstructured.Unstructured{}
	console.SetGroupVersionKind(consoleOperatorGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
		return err
	}
	plugins, found, err := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
	if err != nil || !found {
		return err
	}
	out := make([]string, 0, len(plugins))
	for _, p := range plugins {
		if p != pluginName {
			out = append(out, p)
		}
	}
	_ = unstructured.SetNestedStringSlice(console.Object, out, "spec", "plugins")
	return r.Update(ctx, console)
}

func (r *DesktopPortalReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&guacamolev1alpha1.DesktopPortal{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Complete(r)
}
