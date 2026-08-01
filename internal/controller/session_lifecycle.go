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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

const (
	sessionLifecyclePollInterval            = 20 * time.Second
	defaultIdleSecondsAfterDisconnect int64 = 900
)

type sessionLifecycleDecision struct {
	Phase           guacamolev1alpha1.DesktopSessionPhase
	ConnectionState guacamolev1alpha1.DesktopSessionConnectionState
	ActiveTunnels   int32
	LastActiveAt    *metav1.Time
	IdleSince       *metav1.Time
	Message         string
	Release         bool
	ReleasedReason  string
	RequeueAfter    time.Duration
	MarkVMInUse     bool
}

func effectiveIdleSeconds(
	session *guacamolev1alpha1.DesktopSession,
	pool *guacamolev1alpha1.DesktopPool,
) (seconds int64, enabled bool) {
	if session.Spec.IdleSecondsAfterDisconnect != nil {
		return *session.Spec.IdleSecondsAfterDisconnect, *session.Spec.IdleSecondsAfterDisconnect > 0
	}
	if pool.Spec.SessionLifecycle == nil {
		return 0, false
	}
	if pool.Spec.SessionLifecycle.IdleSecondsAfterDisconnect != nil {
		s := *pool.Spec.SessionLifecycle.IdleSecondsAfterDisconnect
		return s, s > 0
	}
	// Object present but field unset → CRD default 900.
	return defaultIdleSecondsAfterDisconnect, true
}

func effectiveMaxTTLSeconds(
	session *guacamolev1alpha1.DesktopSession,
	pool *guacamolev1alpha1.DesktopPool,
) (seconds int64, enabled bool) {
	if session.Spec.TTLSecondsAfterReady != nil {
		return *session.Spec.TTLSecondsAfterReady, *session.Spec.TTLSecondsAfterReady > 0
	}
	if pool.Spec.SessionLifecycle == nil || pool.Spec.SessionLifecycle.MaxSecondsAfterReady == nil {
		return 0, false
	}
	s := *pool.Spec.SessionLifecycle.MaxSecondsAfterReady
	return s, s > 0
}

// evaluateSessionLifecycle decides phase / idle release from tunnel count and clocks.
func evaluateSessionLifecycle(
	now time.Time,
	session *guacamolev1alpha1.DesktopSession,
	pool *guacamolev1alpha1.DesktopPool,
	activeTunnels int32,
	everConnected bool,
) sessionLifecycleDecision {
	dec := sessionLifecycleDecision{
		ActiveTunnels: activeTunnels,
		RequeueAfter:  sessionLifecyclePollInterval,
	}

	idleSec, idleEnabled := effectiveIdleSeconds(session, pool)
	ttlSec, ttlEnabled := effectiveMaxTTLSeconds(session, pool)

	readyAt := time.Time{}
	if session.Status.ReadyAt != nil {
		readyAt = session.Status.ReadyAt.Time
	}

	if ttlEnabled && !readyAt.IsZero() {
		deadline := readyAt.Add(time.Duration(ttlSec) * time.Second)
		if !now.Before(deadline) {
			dec.Release = true
			dec.ReleasedReason = guacamolev1alpha1.DesktopSessionReleasedMaxTTL
			dec.Phase = guacamolev1alpha1.DesktopSessionPhaseReleased
			dec.Message = fmt.Sprintf("released after max session time (%ds)", ttlSec)
			dec.ConnectionState = guacamolev1alpha1.DesktopSessionConnectionDisconnected
			return dec
		}
		remaining := time.Until(deadline)
		if remaining < dec.RequeueAfter {
			dec.RequeueAfter = remaining
		}
	}

	if activeTunnels > 0 {
		ts := metav1.NewTime(now)
		dec.Phase = guacamolev1alpha1.DesktopSessionPhaseInUse
		dec.ConnectionState = guacamolev1alpha1.DesktopSessionConnectionConnected
		dec.LastActiveAt = &ts
		dec.IdleSince = nil
		dec.MarkVMInUse = true
		dec.Message = fmt.Sprintf("%d active Guacamole tunnel(s)", activeTunnels)
		return dec
	}

	// No active tunnel.
	idleSince := session.Status.IdleSince
	if idleSince == nil {
		// Start idle clock at ReadyAt if never connected, else now (just disconnected).
		if !everConnected && !readyAt.IsZero() {
			t := metav1.NewTime(readyAt)
			idleSince = &t
		} else if session.Status.LastActiveAt != nil {
			t := *session.Status.LastActiveAt
			idleSince = &t
		} else {
			t := metav1.NewTime(now)
			idleSince = &t
		}
	}
	dec.IdleSince = idleSince
	dec.LastActiveAt = session.Status.LastActiveAt

	if everConnected || session.Status.ConnectionState == guacamolev1alpha1.DesktopSessionConnectionConnected ||
		session.Status.Phase == guacamolev1alpha1.DesktopSessionPhaseInUse ||
		session.Status.Phase == guacamolev1alpha1.DesktopSessionPhaseDisconnected {
		dec.Phase = guacamolev1alpha1.DesktopSessionPhaseDisconnected
		dec.ConnectionState = guacamolev1alpha1.DesktopSessionConnectionDisconnected
		dec.Message = "Guacamole tunnel disconnected; waiting for reconnect or idle logoff"
	} else {
		dec.Phase = guacamolev1alpha1.DesktopSessionPhaseReady
		dec.ConnectionState = guacamolev1alpha1.DesktopSessionConnectionNone
		dec.Message = "desktop ready; waiting for user to connect"
	}

	if idleEnabled && idleSince != nil {
		deadline := idleSince.Time.Add(time.Duration(idleSec) * time.Second)
		if !now.Before(deadline) {
			dec.Release = true
			dec.ReleasedReason = guacamolev1alpha1.DesktopSessionReleasedIdleTimeout
			dec.Phase = guacamolev1alpha1.DesktopSessionPhaseReleased
			dec.Message = fmt.Sprintf("released after idle timeout (%ds without tunnel)", idleSec)
			return dec
		}
		remaining := time.Until(deadline)
		if remaining < dec.RequeueAfter {
			dec.RequeueAfter = remaining
		}
		dec.Message = fmt.Sprintf("%s (idle logoff in %s)", dec.Message, remaining.Round(time.Second))
	}

	if dec.RequeueAfter < 5*time.Second {
		dec.RequeueAfter = 5 * time.Second
	}
	return dec
}

func (r *DesktopSessionReconciler) countActiveTunnels(
	ctx context.Context,
	pool *guacamolev1alpha1.DesktopPool,
	connectionID int64,
	connectionName string,
) (int32, error) {
	guacName := pool.Spec.Guacamole.InstanceRef.Name
	if guacName == "" {
		return 0, fmt.Errorf("pool guacamole.instanceRef.name is empty")
	}
	guacNS := pool.Spec.Guacamole.InstanceRef.Namespace
	if guacNS == "" {
		guacNS = pool.Namespace
	}
	guac := &guacamolev1alpha1.Guacamole{}
	if err := r.Get(ctx, types.NamespacedName{Name: guacName, Namespace: guacNS}, guac); err != nil {
		return 0, err
	}
	creds, err := resolveMySQLCredentials(ctx, r.Client, guac)
	if err != nil {
		return 0, err
	}
	db, err := openMySQL(creds)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var count int32
	// Prefer connection_id — Guacamole history stores the display name as connection_name,
	// which differs from the GuacamoleConnection CR metadata.name.
	if connectionID > 0 {
		err = db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM guacamole_connection_history
			WHERE end_date IS NULL AND connection_id = ?`,
			connectionID,
		).Scan(&count)
	} else if connectionName != "" {
		err = db.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM guacamole_connection_history
			WHERE end_date IS NULL AND connection_name = ?`,
			connectionName,
		).Scan(&count)
	} else {
		return 0, fmt.Errorf("no connection id or name to poll tunnels")
	}
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (r *DesktopSessionReconciler) sessionGuacamoleConnectionIdentity(
	ctx context.Context,
	session *guacamolev1alpha1.DesktopSession,
) (connectionID int64, connectionName string, err error) {
	conn := &guacamolev1alpha1.GuacamoleConnection{}
	if err := r.Get(ctx, types.NamespacedName{Name: session.Name, Namespace: session.Namespace}, conn); err != nil {
		if apierrors.IsNotFound(err) {
			// Fall back to CR name only; may not match MySQL until connection exists.
			return 0, session.Name, nil
		}
		return 0, "", err
	}
	name := connectionDisplayName(conn)
	if name == "" {
		name = conn.Name
	}
	return conn.Status.ConnectionID, name, nil
}

func (r *DesktopSessionReconciler) reconcileSessionLifecycle(
	ctx context.Context,
	session *guacamolev1alpha1.DesktopSession,
	pool *guacamolev1alpha1.DesktopPool,
) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	connID, connName, err := r.sessionGuacamoleConnectionIdentity(ctx, session)
	if err != nil {
		return ctrl.Result{}, err
	}

	tunnels, err := r.countActiveTunnels(ctx, pool, connID, connName)
	if err != nil {
		logger.Error(err, "failed to count active Guacamole tunnels; will retry")
		return ctrl.Result{RequeueAfter: sessionLifecyclePollInterval}, nil
	}

	everConnected := session.Status.LastActiveAt != nil ||
		session.Status.Phase == guacamolev1alpha1.DesktopSessionPhaseInUse ||
		session.Status.ConnectionState == guacamolev1alpha1.DesktopSessionConnectionConnected ||
		session.Status.ConnectionState == guacamolev1alpha1.DesktopSessionConnectionDisconnected ||
		tunnels > 0

	dec := evaluateSessionLifecycle(time.Now(), session, pool, tunnels, everConnected)

	if dec.MarkVMInUse {
		_ = r.setDesktopVMSessionState(ctx, session.Namespace, session.Status.DesktopName, guacamolev1alpha1.DesktopStateInUse)
	} else if session.Status.DesktopName != "" {
		_ = r.setDesktopVMSessionState(ctx, session.Namespace, session.Status.DesktopName, guacamolev1alpha1.DesktopStateAllocated)
	}

	if dec.Release {
		logger.Info("releasing DesktopSession", "reason", dec.ReleasedReason, "message", dec.Message)
		if err := r.releaseSession(ctx, session, pool); err != nil {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}
		_ = r.patchSessionStatus(ctx, session, func(status *guacamolev1alpha1.DesktopSessionStatus) {
			clearBrokerQueueStatus(status)
			status.Phase = guacamolev1alpha1.DesktopSessionPhaseReleased
			status.ConnectionState = guacamolev1alpha1.DesktopSessionConnectionDisconnected
			status.ActiveTunnels = 0
			status.IdleSince = dec.IdleSince
			if dec.LastActiveAt != nil {
				status.LastActiveAt = dec.LastActiveAt
			}
			status.ReleasedReason = dec.ReleasedReason
			status.Message = dec.Message
			setDesktopSessionCondition(status, "Ready", metav1.ConditionFalse, dec.ReleasedReason, dec.Message)
		})
		return ctrl.Result{}, nil
	}

	if err := r.patchSessionStatus(ctx, session, func(status *guacamolev1alpha1.DesktopSessionStatus) {
		clearBrokerQueueStatus(status)
		status.Phase = dec.Phase
		status.ConnectionState = dec.ConnectionState
		status.ActiveTunnels = dec.ActiveTunnels
		status.IdleSince = dec.IdleSince
		if dec.LastActiveAt != nil {
			status.LastActiveAt = dec.LastActiveAt
		}
		status.ConnectionName = connName
		status.ServiceDNS = rdpServiceDNS(session.Status.DesktopName, session.Namespace)
		status.Message = dec.Message
		setDesktopSessionCondition(status, "Ready", metav1.ConditionTrue, string(dec.Phase), dec.Message)
	}); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: dec.RequeueAfter}, nil
}

func (r *DesktopSessionReconciler) setDesktopVMSessionState(
	ctx context.Context,
	namespace, vmName string,
	state guacamolev1alpha1.DesktopState,
) error {
	if vmName == "" {
		return nil
	}
	vm := &unstructured.Unstructured{}
	vm.SetGroupVersionKind(virtualMachineGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: vmName, Namespace: namespace}, vm); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	labels := vm.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	if labels[guacamolev1alpha1.DesktopLabelState] == string(state) {
		return nil
	}
	// Only flip between Allocated and InUse for session-owned desktops.
	cur := labels[guacamolev1alpha1.DesktopLabelState]
	if cur != string(guacamolev1alpha1.DesktopStateAllocated) &&
		cur != string(guacamolev1alpha1.DesktopStateInUse) {
		return nil
	}
	labels[guacamolev1alpha1.DesktopLabelState] = string(state)
	vm.SetLabels(labels)
	return r.Update(ctx, vm)
}

// applyPoolLifecycleDefaults copies pool sessionLifecycle defaults onto a session
// create payload when the session fields are unset. Used by the portal API.
func applyPoolLifecycleDefaults(
	sessionSpec map[string]interface{},
	pool *guacamolev1alpha1.DesktopPool,
) {
	if pool == nil || pool.Spec.SessionLifecycle == nil {
		return
	}
	sl := pool.Spec.SessionLifecycle
	if _, ok := sessionSpec["ttlSecondsAfterReady"]; !ok && sl.MaxSecondsAfterReady != nil && *sl.MaxSecondsAfterReady > 0 {
		sessionSpec["ttlSecondsAfterReady"] = *sl.MaxSecondsAfterReady
	}
	if _, ok := sessionSpec["idleSecondsAfterDisconnect"]; !ok {
		if sl.IdleSecondsAfterDisconnect != nil {
			sessionSpec["idleSecondsAfterDisconnect"] = *sl.IdleSecondsAfterDisconnect
		} else {
			sessionSpec["idleSecondsAfterDisconnect"] = defaultIdleSecondsAfterDisconnect
		}
	}
}
