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
	"sort"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

const desktopSessionFinalizer = "guacamole.guacamole.io/desktopsession-finalizer"

// DesktopSessionReconciler allocates an exclusive desktop from a DesktopPool.
type DesktopSessionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktopsessions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktopsessions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktopsessions/finalizers,verbs=update
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktoppools,verbs=get;list;watch
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=guacamoleconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets;services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines;virtualmachineinstances,verbs=get;list;watch;create;update;patch;delete

func (r *DesktopSessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	session := &guacamolev1alpha1.DesktopSession{}
	if err := r.Get(ctx, req.NamespacedName, session); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !session.DeletionTimestamp.IsZero() {
		return r.finalizeSession(ctx, session)
	}

	if !controllerutil.ContainsFinalizer(session, desktopSessionFinalizer) {
		controllerutil.AddFinalizer(session, desktopSessionFinalizer)
		if err := r.Update(ctx, session); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if session.Spec.PoolRef.Name == "" {
		return r.failSession(ctx, session, "spec.poolRef.name is required")
	}
	if session.Spec.Requester.Subject == "" {
		return r.failSession(ctx, session, "spec.requester.subject is required")
	}

	if session.Status.Phase == guacamolev1alpha1.DesktopSessionPhaseReleased {
		return ctrl.Result{}, nil
	}

	pool := &guacamolev1alpha1.DesktopPool{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      session.Spec.PoolRef.Name,
		Namespace: session.Namespace,
	}, pool); err != nil {
		if apierrors.IsNotFound(err) {
			return r.failSession(ctx, session, fmt.Sprintf("DesktopPool %q not found", session.Spec.PoolRef.Name))
		}
		return ctrl.Result{}, err
	}

	// Already allocated: ensure connection + TTL.
	if session.Status.DesktopName != "" &&
		(session.Status.Phase == guacamolev1alpha1.DesktopSessionPhaseReady ||
			session.Status.Phase == guacamolev1alpha1.DesktopSessionPhasePending) {
		if err := r.ensureSessionConnection(ctx, session, pool); err != nil {
			logger.Error(err, "failed to ensure session GuacamoleConnection")
			_ = r.patchSessionStatus(ctx, session, func(status *guacamolev1alpha1.DesktopSessionStatus) {
				status.Message = err.Error()
				setDesktopSessionCondition(status, "Ready", metav1.ConditionFalse, "ConnectionFailed", err.Error())
			})
			return ctrl.Result{RequeueAfter: 20 * time.Second}, err
		}

		now := metav1.Now()
		if err := r.patchSessionStatus(ctx, session, func(status *guacamolev1alpha1.DesktopSessionStatus) {
			status.Phase = guacamolev1alpha1.DesktopSessionPhaseReady
			status.ConnectionName = session.Name
			status.ServiceDNS = rdpServiceDNS(session.Status.DesktopName, session.Namespace)
			status.Message = ""
			if status.ReadyAt == nil {
				status.ReadyAt = &now
			}
			setDesktopSessionCondition(status, "Ready", metav1.ConditionTrue, "Allocated",
				fmt.Sprintf("desktop %s allocated to %s", status.DesktopName, session.Spec.Requester.Subject))
		}); err != nil {
			return ctrl.Result{}, err
		}

		if expired, requeueAfter := sessionTTLExpired(session); expired {
			logger.Info("DesktopSession TTL expired; releasing", "ttl", *session.Spec.TTLSecondsAfterReady)
			if err := r.releaseSession(ctx, session, pool); err != nil {
				return ctrl.Result{RequeueAfter: 10 * time.Second}, err
			}
			_ = r.patchSessionStatus(ctx, session, func(status *guacamolev1alpha1.DesktopSessionStatus) {
				status.Phase = guacamolev1alpha1.DesktopSessionPhaseReleased
				status.Message = "released after TTL"
				setDesktopSessionCondition(status, "Ready", metav1.ConditionFalse, "TTLExpired", "session released after TTL")
			})
			return ctrl.Result{}, nil
		} else if requeueAfter > 0 {
			return ctrl.Result{RequeueAfter: requeueAfter}, nil
		}
		return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
	}

	// Allocate a free desktop.
	vmName, err := r.reserveAvailableDesktop(ctx, session, pool)
	if err != nil {
		logger.Info("waiting for available desktop", "error", err.Error())
		_ = r.patchSessionStatus(ctx, session, func(status *guacamolev1alpha1.DesktopSessionStatus) {
			status.Phase = guacamolev1alpha1.DesktopSessionPhasePending
			status.Message = err.Error()
			setDesktopSessionCondition(status, "Ready", metav1.ConditionFalse, "WaitingForDesktop", err.Error())
		})
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	if err := r.patchSessionStatus(ctx, session, func(status *guacamolev1alpha1.DesktopSessionStatus) {
		status.DesktopName = vmName
		status.ServiceDNS = rdpServiceDNS(vmName, session.Namespace)
		status.Phase = guacamolev1alpha1.DesktopSessionPhasePending
		status.Message = "desktop reserved; creating GuacamoleConnection"
		setDesktopSessionCondition(status, "Ready", metav1.ConditionFalse, "Reserving", "desktop reserved")
	}); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{Requeue: true}, nil
}

func (r *DesktopSessionReconciler) reserveAvailableDesktop(
	ctx context.Context,
	session *guacamolev1alpha1.DesktopSession,
	pool *guacamolev1alpha1.DesktopPool,
) (string, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "kubevirt.io",
		Version: "v1",
		Kind:    "VirtualMachineList",
	})
	if err := r.List(ctx, list, client.InNamespace(pool.Namespace), client.MatchingLabels{
		guacamolev1alpha1.DesktopLabelPool: pool.Name,
	}); err != nil {
		return "", err
	}

	candidates := make([]unstructured.Unstructured, 0)
	for i := range list.Items {
		vm := &list.Items[i]
		labels := vm.GetLabels()
		if labels == nil {
			continue
		}
		if labels[guacamolev1alpha1.DesktopLabelState] != string(guacamolev1alpha1.DesktopStateAvailable) {
			continue
		}
		// Skip VMs already claimed by another session label (stale Available).
		if sess := labels[guacamolev1alpha1.DesktopLabelSession]; sess != "" && sess != session.Name {
			continue
		}
		candidates = append(candidates, *vm)
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no Available desktop in pool %q", pool.Name)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].GetName() < candidates[j].GetName()
	})

	for i := range candidates {
		vm := &candidates[i]
		// Re-get for fresh resourceVersion.
		fresh := &unstructured.Unstructured{}
		fresh.SetGroupVersionKind(virtualMachineGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: vm.GetName(), Namespace: vm.GetNamespace()}, fresh); err != nil {
			continue
		}
		labels := fresh.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		if labels[guacamolev1alpha1.DesktopLabelState] != string(guacamolev1alpha1.DesktopStateAvailable) {
			continue
		}
		if sess := labels[guacamolev1alpha1.DesktopLabelSession]; sess != "" && sess != session.Name {
			continue
		}
		labels[guacamolev1alpha1.DesktopLabelState] = string(guacamolev1alpha1.DesktopStateAllocated)
		labels[guacamolev1alpha1.DesktopLabelSession] = session.Name
		labels[guacamolev1alpha1.DesktopLabelRequester] = sanitizeLabelValue(session.Spec.Requester.Subject)
		fresh.SetLabels(labels)
		if err := r.Update(ctx, fresh); err != nil {
			if apierrors.IsConflict(err) {
				continue
			}
			return "", err
		}

		// Drop provisional pool GuacamoleConnection named after the VM, if any.
		provisional := &guacamolev1alpha1.GuacamoleConnection{}
		if err := r.Get(ctx, types.NamespacedName{Name: fresh.GetName(), Namespace: pool.Namespace}, provisional); err == nil {
			_ = r.Delete(ctx, provisional)
		}

		return fresh.GetName(), nil
	}
	return "", fmt.Errorf("failed to reserve an Available desktop (contention); will retry")
}

func (r *DesktopSessionReconciler) ensureSessionConnection(
	ctx context.Context,
	session *guacamolev1alpha1.DesktopSession,
	pool *guacamolev1alpha1.DesktopPool,
) error {
	credRef, err := resolveDesktopCredentials(ctx, r.Client, r.Scheme, pool, pool)
	if err != nil {
		return err
	}

	vmName := session.Status.DesktopName
	ignoreCert := true
	if pool.Spec.Guacamole.IgnoreCert != nil {
		ignoreCert = *pool.Spec.Guacamole.IgnoreCert
	}
	security := pool.Spec.Guacamole.Security
	if security == "" {
		security = "any"
	}
	username := pool.Spec.Guacamole.Username
	if username == "" {
		username = "Administrator"
	}
	port := desktopRDPPort(pool)
	host := fmt.Sprintf("%s.%s.svc", rdpServiceName(vmName), pool.Namespace)

	conn := &guacamolev1alpha1.GuacamoleConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      session.Name,
			Namespace: session.Namespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, conn, func() error {
		if err := controllerutil.SetControllerReference(session, conn, r.Scheme); err != nil {
			return err
		}
		if conn.Labels == nil {
			conn.Labels = map[string]string{}
		}
		conn.Labels[guacamolev1alpha1.DesktopLabelPool] = pool.Name
		conn.Labels[guacamolev1alpha1.DesktopLabelVM] = vmName
		conn.Labels[guacamolev1alpha1.DesktopLabelSession] = session.Name
		conn.Labels[guacamolev1alpha1.DesktopLabelManagedBy] = guacamolev1alpha1.DesktopManagedByValue
		conn.Labels[guacamolev1alpha1.DesktopLabelRequester] = sanitizeLabelValue(session.Spec.Requester.Subject)

		conn.Spec.GuacamoleRef = pool.Spec.Guacamole.InstanceRef
		conn.Spec.DisplayName = fmt.Sprintf("%s (%s)", session.Spec.Requester.Subject, vmName)
		conn.Spec.Protocol = "rdp"
		conn.Spec.ParentGroup = pool.Spec.Guacamole.ParentGroup
		// Guacamole hides connections from non-admin users unless READ is granted.
		conn.Spec.Permissions = []guacamolev1alpha1.ConnectionPermissionSpec{{
			EntityName: session.Spec.Requester.Subject,
			EntityType: "USER",
			Permission: "READ",
		}}
		conn.Spec.RDP = &guacamolev1alpha1.RDPConnectionSpec{
			Hostname:          host,
			Port:              &port,
			Username:          username,
			Security:          security,
			IgnoreCert:        &ignoreCert,
			PasswordSecretRef: credRef,
		}
		return nil
	})
	return err
}

func (r *DesktopSessionReconciler) releaseSession(
	ctx context.Context,
	session *guacamolev1alpha1.DesktopSession,
	pool *guacamolev1alpha1.DesktopPool,
) error {
	// Delete session-owned connection.
	conn := &guacamolev1alpha1.GuacamoleConnection{}
	if err := r.Get(ctx, types.NamespacedName{Name: session.Name, Namespace: session.Namespace}, conn); err == nil {
		if err := r.Delete(ctx, conn); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	vmName := session.Status.DesktopName
	if vmName == "" {
		return nil
	}

	policy := pool.Spec.RecyclePolicy
	if policy == "" {
		policy = "Delete"
	}

	switch policy {
	case "Retain":
		vm := &unstructured.Unstructured{}
		vm.SetGroupVersionKind(virtualMachineGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: vmName, Namespace: session.Namespace}, vm); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		labels := vm.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		delete(labels, guacamolev1alpha1.DesktopLabelSession)
		delete(labels, guacamolev1alpha1.DesktopLabelRequester)
		labels[guacamolev1alpha1.DesktopLabelState] = string(guacamolev1alpha1.DesktopStateAvailable)
		vm.SetLabels(labels)
		return r.Update(ctx, vm)
	default: // Delete
		return deleteDesktopResources(ctx, r.Client, session.Namespace, vmName)
	}
}

func (r *DesktopSessionReconciler) finalizeSession(ctx context.Context, session *guacamolev1alpha1.DesktopSession) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(session, desktopSessionFinalizer) {
		return ctrl.Result{}, nil
	}

	pool := &guacamolev1alpha1.DesktopPool{}
	if session.Spec.PoolRef.Name != "" {
		if err := r.Get(ctx, types.NamespacedName{
			Name:      session.Spec.PoolRef.Name,
			Namespace: session.Namespace,
		}, pool); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	if session.Status.DesktopName != "" {
		if err := r.releaseSession(ctx, session, pool); err != nil {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}
	}

	controllerutil.RemoveFinalizer(session, desktopSessionFinalizer)
	if err := r.Update(ctx, session); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func sessionTTLExpired(session *guacamolev1alpha1.DesktopSession) (bool, time.Duration) {
	if session.Spec.TTLSecondsAfterReady == nil || *session.Spec.TTLSecondsAfterReady <= 0 {
		return false, 0
	}
	if session.Status.ReadyAt == nil {
		return false, time.Duration(*session.Spec.TTLSecondsAfterReady) * time.Second
	}
	deadline := session.Status.ReadyAt.Add(time.Duration(*session.Spec.TTLSecondsAfterReady) * time.Second)
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return true, 0
	}
	return false, remaining
}

func (r *DesktopSessionReconciler) failSession(ctx context.Context, session *guacamolev1alpha1.DesktopSession, message string) (ctrl.Result, error) {
	if err := r.patchSessionStatus(ctx, session, func(status *guacamolev1alpha1.DesktopSessionStatus) {
		status.Phase = guacamolev1alpha1.DesktopSessionPhaseFailed
		status.Message = message
		setDesktopSessionCondition(status, "Ready", metav1.ConditionFalse, "Failed", message)
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
	if err := r.Status().Update(ctx, latest); err != nil {
		return err
	}
	// Keep caller's status roughly in sync for subsequent steps in the same reconcile.
	session.Status = latest.Status
	return nil
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
		Owns(&guacamolev1alpha1.GuacamoleConnection{}).
		Complete(r)
}
