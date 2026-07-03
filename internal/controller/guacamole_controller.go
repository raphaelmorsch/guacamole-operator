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
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

const guacamoleFinalizer = "guacamole.guacamole.io/finalizer"

// GuacamoleReconciler reconciles a Guacamole object.
type GuacamoleReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=guacamoles,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=guacamoles/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=guacamoles/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;secrets;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete

func (r *GuacamoleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	guac := &guacamolev1alpha1.Guacamole{}
	if err := r.Get(ctx, req.NamespacedName, guac); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !guac.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, guac)
	}

	if !controllerutil.ContainsFinalizer(guac, guacamoleFinalizer) {
		controllerutil.AddFinalizer(guac, guacamoleFinalizer)
		if err := r.Update(ctx, guac); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if err := r.reconcileStack(ctx, guac); err != nil {
		logger.Error(err, "failed to reconcile Guacamole stack")
		r.setStatus(ctx, guac, guacamolev1alpha1.GuacamolePhaseFailed, "", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	routeURL, err := r.routeURL(ctx, guac)
	if err != nil {
		logger.Error(err, "failed to resolve route URL")
		r.setStatus(ctx, guac, guacamolev1alpha1.GuacamolePhaseFailed, "", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	r.setStatus(ctx, guac, guacamolev1alpha1.GuacamolePhaseRunning, routeURL, nil)
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *GuacamoleReconciler) reconcileStack(ctx context.Context, guac *guacamolev1alpha1.Guacamole) error {
	desiredSecret := desiredMySQLSecret(guac)
	secret := &corev1.Secret{}
	secret.Name = desiredSecret.Name
	secret.Namespace = desiredSecret.Namespace
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if err := controllerutil.SetControllerReference(guac, secret, r.Scheme); err != nil {
			return err
		}
		if secret.CreationTimestamp.IsZero() {
			secret.Type = desiredSecret.Type
			secret.StringData = desiredSecret.StringData
		}
		return nil
	}); err != nil {
		return fmt.Errorf("reconcile mysql secret: %w", err)
	}

	desiredPVC := desiredMySQLPVC(guac)
	pvc := &corev1.PersistentVolumeClaim{}
	pvc.Name = desiredPVC.Name
	pvc.Namespace = desiredPVC.Namespace
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, pvc, func() error {
		if err := controllerutil.SetControllerReference(guac, pvc, r.Scheme); err != nil {
			return err
		}
		if pvc.CreationTimestamp.IsZero() {
			pvc.Spec = desiredPVC.Spec
		}
		return nil
	}); err != nil {
		return fmt.Errorf("reconcile mysql pvc: %w", err)
	}

	if err := r.reconcileDeployment(ctx, guac, desiredMySQLDeployment(guac)); err != nil {
		return fmt.Errorf("reconcile mysql deployment: %w", err)
	}
	if err := r.reconcileService(ctx, guac, desiredMySQLService(guac)); err != nil {
		return fmt.Errorf("reconcile mysql service: %w", err)
	}
	if err := r.reconcileDeployment(ctx, guac, desiredGuacdDeployment(guac)); err != nil {
		return fmt.Errorf("reconcile guacd deployment: %w", err)
	}
	if err := r.reconcileService(ctx, guac, desiredGuacdService(guac)); err != nil {
		return fmt.Errorf("reconcile guacd service: %w", err)
	}
	if err := r.reconcileDeployment(ctx, guac, desiredGuacamoleDeployment(guac)); err != nil {
		return fmt.Errorf("reconcile guacamole deployment: %w", err)
	}
	if err := r.reconcileService(ctx, guac, desiredGuacamoleService(guac)); err != nil {
		return fmt.Errorf("reconcile guacamole service: %w", err)
	}

	if routeEnabled(&guac.Spec) {
		if err := r.reconcileRoute(ctx, guac); err != nil {
			return fmt.Errorf("reconcile route: %w", err)
		}
	} else if err := r.deleteRouteIfExists(ctx, guac); err != nil {
		return err
	}

	return nil
}

func (r *GuacamoleReconciler) reconcileDeployment(ctx context.Context, owner *guacamolev1alpha1.Guacamole, desired *appsv1.Deployment) error {
	deploy := &appsv1.Deployment{}
	deploy.Name = desired.Name
	deploy.Namespace = desired.Namespace
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		if err := controllerutil.SetControllerReference(owner, deploy, r.Scheme); err != nil {
			return err
		}
		deploy.Labels = desired.Labels
		deploy.Spec = desired.Spec
		return nil
	})
	return err
}

func (r *GuacamoleReconciler) reconcileService(ctx context.Context, owner *guacamolev1alpha1.Guacamole, desired *corev1.Service) error {
	svc := &corev1.Service{}
	svc.Name = desired.Name
	svc.Namespace = desired.Namespace
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(owner, svc, r.Scheme); err != nil {
			return err
		}
		svc.Labels = desired.Labels
		svc.Spec.Selector = desired.Spec.Selector
		svc.Spec.Ports = desired.Spec.Ports
		svc.Spec.Type = desired.Spec.Type
		return nil
	})
	return err
}

func (r *GuacamoleReconciler) reconcileRoute(ctx context.Context, owner *guacamolev1alpha1.Guacamole) error {
	desired := desiredRoute(owner)
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(routeGVK)
	route.SetName(desired.GetName())
	route.SetNamespace(desired.GetNamespace())

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, route, func() error {
		if err := controllerutil.SetControllerReference(owner, route, r.Scheme); err != nil {
			return err
		}
		route.SetLabels(desired.GetLabels())
		spec, found, err := unstructured.NestedMap(desired.Object, "spec")
		if err != nil {
			return err
		}
		if found {
			if err := unstructured.SetNestedMap(route.Object, spec, "spec"); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func (r *GuacamoleReconciler) deleteRouteIfExists(ctx context.Context, guac *guacamolev1alpha1.Guacamole) error {
	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(routeGVK)
	err := r.Get(ctx, types.NamespacedName{
		Name:      routeName(guac.Name),
		Namespace: guac.Namespace,
	}, route)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.Delete(ctx, route)
}

func (r *GuacamoleReconciler) routeURL(ctx context.Context, guac *guacamolev1alpha1.Guacamole) (string, error) {
	if !routeEnabled(&guac.Spec) {
		return "", nil
	}

	route := &unstructured.Unstructured{}
	route.SetGroupVersionKind(routeGVK)
	if err := r.Get(ctx, types.NamespacedName{
		Name:      routeName(guac.Name),
		Namespace: guac.Namespace,
	}, route); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}

	if host := routeHost(route); host != "" {
		return "https://" + host, nil
	}
	return "", nil
}

func (r *GuacamoleReconciler) setStatus(
	ctx context.Context,
	guac *guacamolev1alpha1.Guacamole,
	phase guacamolev1alpha1.GuacamolePhase,
	routeURL string,
	reconcileErr error,
) {
	latest := &guacamolev1alpha1.Guacamole{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      guac.Name,
		Namespace: guac.Namespace,
	}, latest); err != nil {
		return
	}

	now := metav1.Now()
	condition := metav1.Condition{
		Type:               "Ready",
		LastTransitionTime: now,
		ObservedGeneration: latest.GetGeneration(),
	}

	if reconcileErr != nil {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "ReconcileFailed"
		condition.Message = reconcileErr.Error()
	} else if phase == guacamolev1alpha1.GuacamolePhaseRunning {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Reconciled"
		condition.Message = "Guacamole stack is running"
	} else {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "Pending"
		condition.Message = "Guacamole stack is being provisioned"
	}

	latest.Status.Phase = phase
	latest.Status.RouteURL = routeURL
	latest.Status.Conditions = mergeCondition(latest.Status.Conditions, condition)

	_ = r.Status().Update(ctx, latest)
}

func mergeCondition(conditions []metav1.Condition, desired metav1.Condition) []metav1.Condition {
	for i, c := range conditions {
		if c.Type == desired.Type {
			if c.Status == desired.Status && c.Reason == desired.Reason && c.Message == desired.Message {
				return conditions
			}
			conditions[i] = desired
			return conditions
		}
	}
	return append(conditions, desired)
}

func (r *GuacamoleReconciler) finalize(ctx context.Context, guac *guacamolev1alpha1.Guacamole) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(guac, guacamoleFinalizer) {
		return ctrl.Result{}, nil
	}

	controllerutil.RemoveFinalizer(guac, guacamoleFinalizer)
	if err := r.Update(ctx, guac); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GuacamoleReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&guacamolev1alpha1.Guacamole{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}
