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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

const guacamoleConnectionFinalizer = "guacamole.guacamole.io/connection-finalizer"

// GuacamoleConnectionReconciler reconciles a GuacamoleConnection object.
type GuacamoleConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=guacamoleconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=guacamoleconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=guacamoleconnections/finalizers,verbs=update
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=guacamoles,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *GuacamoleConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	conn := &guacamolev1alpha1.GuacamoleConnection{}
	if err := r.Get(ctx, req.NamespacedName, conn); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !conn.DeletionTimestamp.IsZero() {
		return r.finalizeConnection(ctx, conn)
	}

	if !controllerutil.ContainsFinalizer(conn, guacamoleConnectionFinalizer) {
		controllerutil.AddFinalizer(conn, guacamoleConnectionFinalizer)
		if err := r.Update(ctx, conn); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if err := validateConnectionSpec(conn); err != nil {
		logger.Error(err, "invalid GuacamoleConnection spec")
		r.setConnectionStatus(ctx, conn, guacamolev1alpha1.GuacamoleConnectionPhaseFailed, 0, err)
		return ctrl.Result{}, err
	}

	connectionID, err := r.reconcileConnectionInDatabase(ctx, conn)
	if err != nil {
		logger.Error(err, "failed to reconcile Guacamole connection in database")
		r.setConnectionStatus(ctx, conn, guacamolev1alpha1.GuacamoleConnectionPhaseFailed, conn.Status.ConnectionID, err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}

	r.setConnectionStatus(ctx, conn, guacamolev1alpha1.GuacamoleConnectionPhaseReady, connectionID, nil)
	return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

func (r *GuacamoleConnectionReconciler) reconcileConnectionInDatabase(
	ctx context.Context,
	conn *guacamolev1alpha1.GuacamoleConnection,
) (int64, error) {
	guac, err := r.getParentGuacamole(ctx, conn)
	if err != nil {
		return 0, err
	}
	if guac.Status.Phase != guacamolev1alpha1.GuacamolePhaseRunning {
		return 0, fmt.Errorf("parent Guacamole %s/%s is not running (phase=%s)",
			guac.Namespace, guac.Name, guac.Status.Phase)
	}

	creds, err := r.resolveMySQLCredentials(ctx, guac)
	if err != nil {
		return 0, err
	}

	db, err := openMySQL(creds)
	if err != nil {
		return 0, fmt.Errorf("open mysql: %w", err)
	}
	defer db.Close()

	if err := r.waitForDatabase(ctx, db); err != nil {
		return 0, err
	}

	return r.upsertConnection(ctx, db, conn)
}

func (r *GuacamoleConnectionReconciler) getParentGuacamole(
	ctx context.Context,
	conn *guacamolev1alpha1.GuacamoleConnection,
) (*guacamolev1alpha1.Guacamole, error) {
	guac := &guacamolev1alpha1.Guacamole{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      conn.Spec.GuacamoleRef.Name,
		Namespace: guacamoleInstanceNamespace(conn),
	}, guac); err != nil {
		return nil, fmt.Errorf("get parent Guacamole %s: %w", conn.Spec.GuacamoleRef.Name, err)
	}
	return guac, nil
}

func (r *GuacamoleConnectionReconciler) setConnectionStatus(
	ctx context.Context,
	conn *guacamolev1alpha1.GuacamoleConnection,
	phase guacamolev1alpha1.GuacamoleConnectionPhase,
	connectionID int64,
	reconcileErr error,
) {
	latest := &guacamolev1alpha1.GuacamoleConnection{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      conn.Name,
		Namespace: conn.Namespace,
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
	} else if phase == guacamolev1alpha1.GuacamoleConnectionPhaseReady {
		condition.Status = metav1.ConditionTrue
		condition.Reason = "Reconciled"
		condition.Message = "Guacamole connection is synchronized"
	} else {
		condition.Status = metav1.ConditionFalse
		condition.Reason = "Pending"
		condition.Message = "Guacamole connection is being provisioned"
	}

	latest.Status.Phase = phase
	if connectionID > 0 {
		latest.Status.ConnectionID = connectionID
	}
	latest.Status.Conditions = mergeCondition(latest.Status.Conditions, condition)
	_ = r.Status().Update(ctx, latest)
}

func (r *GuacamoleConnectionReconciler) finalizeConnection(
	ctx context.Context,
	conn *guacamolev1alpha1.GuacamoleConnection,
) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(conn, guacamoleConnectionFinalizer) {
		return ctrl.Result{}, nil
	}

	if conn.Status.ConnectionID > 0 {
		if guac, err := r.getParentGuacamole(ctx, conn); err == nil {
			if creds, err := r.resolveMySQLCredentials(ctx, guac); err == nil {
				if db, err := openMySQL(creds); err == nil {
					_ = deleteConnection(ctx, db, conn.Status.ConnectionID)
					_ = db.Close()
				}
			}
		}
	}

	controllerutil.RemoveFinalizer(conn, guacamoleConnectionFinalizer)
	if err := r.Update(ctx, conn); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GuacamoleConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&guacamolev1alpha1.GuacamoleConnection{}).
		Complete(r)
}
