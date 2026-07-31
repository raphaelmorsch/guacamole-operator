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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

// DesktopSessionReconciler reconciles a DesktopSession object.
// Full allocation (reserve Available VM → GuacamoleConnection) is post-MVP.
// This controller currently reports Pending until DesktopPool createConnections=false
// and allocation logic is enabled.
type DesktopSessionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktopsessions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktopsessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktopsessions/finalizers,verbs=update
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktoppools,verbs=get;list;watch

func (r *DesktopSessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	session := &guacamolev1alpha1.DesktopSession{}
	if err := r.Get(ctx, req.NamespacedName, session); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if session.Spec.PoolRef.Name == "" {
		return r.setSessionStatus(ctx, session,
			guacamolev1alpha1.DesktopSessionPhaseFailed,
			"spec.poolRef.name is required")
	}
	if session.Spec.Requester.Subject == "" {
		return r.setSessionStatus(ctx, session,
			guacamolev1alpha1.DesktopSessionPhaseFailed,
			"spec.requester.subject is required")
	}

	pool := &guacamolev1alpha1.DesktopPool{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      session.Spec.PoolRef.Name,
		Namespace: session.Namespace,
	}, pool); err != nil {
		if apierrors.IsNotFound(err) {
			return r.setSessionStatus(ctx, session,
				guacamolev1alpha1.DesktopSessionPhaseFailed,
				fmt.Sprintf("DesktopPool %q not found", session.Spec.PoolRef.Name))
		}
		return ctrl.Result{}, err
	}

	// Allocation is intentionally deferred. DesktopPool MVP creates provisional
	// GuacamoleConnections for Available VMs (createConnections=true).
	logger.Info("DesktopSession accepted; allocation controller not yet enabled",
		"pool", pool.Name, "subject", session.Spec.Requester.Subject)

	if session.Status.Phase == guacamolev1alpha1.DesktopSessionPhaseReady {
		return ctrl.Result{}, nil
	}

	msg := "waiting for DesktopSession allocation (enable after DesktopPool MVP)"
	if err := r.patchSessionStatus(ctx, session, func(status *guacamolev1alpha1.DesktopSessionStatus) {
		status.Phase = guacamolev1alpha1.DesktopSessionPhasePending
		status.Message = msg
		setDesktopSessionCondition(status, "Ready", metav1.ConditionFalse, "AllocationPending", msg)
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
}

func (r *DesktopSessionReconciler) setSessionStatus(
	ctx context.Context,
	session *guacamolev1alpha1.DesktopSession,
	phase guacamolev1alpha1.DesktopSessionPhase,
	message string,
) (ctrl.Result, error) {
	if err := r.patchSessionStatus(ctx, session, func(status *guacamolev1alpha1.DesktopSessionStatus) {
		status.Phase = phase
		status.Message = message
		cond := metav1.ConditionFalse
		reason := "Pending"
		if phase == guacamolev1alpha1.DesktopSessionPhaseFailed {
			reason = "Failed"
		}
		if phase == guacamolev1alpha1.DesktopSessionPhaseReady {
			cond = metav1.ConditionTrue
			reason = "Ready"
		}
		setDesktopSessionCondition(status, "Ready", cond, reason, message)
	}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *DesktopSessionReconciler) patchSessionStatus(
	ctx context.Context,
	session *guacamolev1alpha1.DesktopSession,
	mutate func(*guacamolev1alpha1.DesktopSessionStatus),
) error {
	latest := &guacamolev1alpha1.DesktopSession{}
	if err := r.Get(ctx, types.NamespacedName{Name: session.Name, Namespace: session.Namespace}, latest); err != nil {
		return err
	}
	mutate(&latest.Status)
	return r.Status().Update(ctx, latest)
}

func setDesktopSessionCondition(status *guacamolev1alpha1.DesktopSessionStatus, ctype string, condStatus metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	for i := range status.Conditions {
		if status.Conditions[i].Type == ctype {
			status.Conditions[i].Status = condStatus
			status.Conditions[i].Reason = reason
			status.Conditions[i].Message = message
			status.Conditions[i].LastTransitionTime = now
			return
		}
	}
	status.Conditions = append(status.Conditions, metav1.Condition{
		Type:               ctype,
		Status:             condStatus,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	})
}

// SetupWithManager sets up the controller with the Manager.
func (r *DesktopSessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&guacamolev1alpha1.DesktopSession{}).
		Complete(r)
}
