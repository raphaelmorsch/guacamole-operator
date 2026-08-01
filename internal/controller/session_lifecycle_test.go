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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

func int64Ptr(v int64) *int64 { return &v }

func TestEvaluateSessionLifecycleInUse(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	readyAt := metav1.NewTime(now.Add(-10 * time.Minute))
	session := &guacamolev1alpha1.DesktopSession{
		Status: guacamolev1alpha1.DesktopSessionStatus{
			Phase:   guacamolev1alpha1.DesktopSessionPhaseReady,
			ReadyAt: &readyAt,
		},
	}
	pool := &guacamolev1alpha1.DesktopPool{
		Spec: guacamolev1alpha1.DesktopPoolSpec{
			SessionLifecycle: &guacamolev1alpha1.DesktopPoolSessionLifecycleSpec{
				IdleSecondsAfterDisconnect: int64Ptr(900),
			},
		},
	}
	dec := evaluateSessionLifecycle(now, session, pool, 1, true)
	if dec.Phase != guacamolev1alpha1.DesktopSessionPhaseInUse {
		t.Fatalf("phase=%s want InUse", dec.Phase)
	}
	if dec.ConnectionState != guacamolev1alpha1.DesktopSessionConnectionConnected {
		t.Fatalf("connectionState=%s", dec.ConnectionState)
	}
	if dec.IdleSince != nil {
		t.Fatalf("IdleSince should be cleared while connected")
	}
	if dec.Release {
		t.Fatalf("should not release while connected")
	}
	if !dec.MarkVMInUse {
		t.Fatalf("expected MarkVMInUse")
	}
}

func TestEvaluateSessionLifecycleIdleTimeoutAfterDisconnect(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	readyAt := metav1.NewTime(now.Add(-30 * time.Minute))
	idleSince := metav1.NewTime(now.Add(-20 * time.Minute))
	session := &guacamolev1alpha1.DesktopSession{
		Status: guacamolev1alpha1.DesktopSessionStatus{
			Phase:           guacamolev1alpha1.DesktopSessionPhaseDisconnected,
			ConnectionState: guacamolev1alpha1.DesktopSessionConnectionDisconnected,
			ReadyAt:         &readyAt,
			IdleSince:       &idleSince,
		},
	}
	pool := &guacamolev1alpha1.DesktopPool{
		Spec: guacamolev1alpha1.DesktopPoolSpec{
			SessionLifecycle: &guacamolev1alpha1.DesktopPoolSessionLifecycleSpec{
				IdleSecondsAfterDisconnect: int64Ptr(900),
			},
		},
	}
	dec := evaluateSessionLifecycle(now, session, pool, 0, true)
	if !dec.Release || dec.ReleasedReason != guacamolev1alpha1.DesktopSessionReleasedIdleTimeout {
		t.Fatalf("expected idle release, got release=%v reason=%s", dec.Release, dec.ReleasedReason)
	}
	if dec.Phase != guacamolev1alpha1.DesktopSessionPhaseReleased {
		t.Fatalf("phase=%s", dec.Phase)
	}
}

func TestEvaluateSessionLifecycleNeverConnectedIdle(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	readyAt := metav1.NewTime(now.Add(-20 * time.Minute))
	session := &guacamolev1alpha1.DesktopSession{
		Status: guacamolev1alpha1.DesktopSessionStatus{
			Phase:   guacamolev1alpha1.DesktopSessionPhaseReady,
			ReadyAt: &readyAt,
		},
	}
	pool := &guacamolev1alpha1.DesktopPool{
		Spec: guacamolev1alpha1.DesktopPoolSpec{
			SessionLifecycle: &guacamolev1alpha1.DesktopPoolSessionLifecycleSpec{
				IdleSecondsAfterDisconnect: int64Ptr(900),
			},
		},
	}
	dec := evaluateSessionLifecycle(now, session, pool, 0, false)
	if !dec.Release || dec.ReleasedReason != guacamolev1alpha1.DesktopSessionReleasedIdleTimeout {
		t.Fatalf("expected never-connected idle release, got release=%v reason=%s msg=%s",
			dec.Release, dec.ReleasedReason, dec.Message)
	}
}

func TestEvaluateSessionLifecycleReconnectClearsIdle(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	readyAt := metav1.NewTime(now.Add(-30 * time.Minute))
	idleSince := metav1.NewTime(now.Add(-10 * time.Minute))
	session := &guacamolev1alpha1.DesktopSession{
		Status: guacamolev1alpha1.DesktopSessionStatus{
			Phase:           guacamolev1alpha1.DesktopSessionPhaseDisconnected,
			ConnectionState: guacamolev1alpha1.DesktopSessionConnectionDisconnected,
			ReadyAt:         &readyAt,
			IdleSince:       &idleSince,
		},
	}
	pool := &guacamolev1alpha1.DesktopPool{
		Spec: guacamolev1alpha1.DesktopPoolSpec{
			SessionLifecycle: &guacamolev1alpha1.DesktopPoolSessionLifecycleSpec{
				IdleSecondsAfterDisconnect: int64Ptr(900),
			},
		},
	}
	dec := evaluateSessionLifecycle(now, session, pool, 2, true)
	if dec.Phase != guacamolev1alpha1.DesktopSessionPhaseInUse {
		t.Fatalf("phase=%s want InUse after reconnect", dec.Phase)
	}
	if dec.IdleSince != nil {
		t.Fatalf("reconnect must clear IdleSince")
	}
	if dec.Release {
		t.Fatalf("must not release while tunnels active")
	}
}

func TestEvaluateSessionLifecycleMaxTTLPrecedence(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	readyAt := metav1.NewTime(now.Add(-2 * time.Hour))
	session := &guacamolev1alpha1.DesktopSession{
		Spec: guacamolev1alpha1.DesktopSessionSpec{
			TTLSecondsAfterReady: int64Ptr(3600),
		},
		Status: guacamolev1alpha1.DesktopSessionStatus{
			Phase:   guacamolev1alpha1.DesktopSessionPhaseInUse,
			ReadyAt: &readyAt,
		},
	}
	pool := &guacamolev1alpha1.DesktopPool{
		Spec: guacamolev1alpha1.DesktopPoolSpec{
			SessionLifecycle: &guacamolev1alpha1.DesktopPoolSessionLifecycleSpec{
				IdleSecondsAfterDisconnect: int64Ptr(900),
				MaxSecondsAfterReady:       int64Ptr(7200),
			},
		},
	}
	// Session TTL (3600) wins over pool max (7200); session is past 1h.
	dec := evaluateSessionLifecycle(now, session, pool, 1, true)
	if !dec.Release || dec.ReleasedReason != guacamolev1alpha1.DesktopSessionReleasedMaxTTL {
		t.Fatalf("expected MaxTTL release, got release=%v reason=%s", dec.Release, dec.ReleasedReason)
	}
}

func TestEffectiveIdleDisabledWithoutPoolBlock(t *testing.T) {
	session := &guacamolev1alpha1.DesktopSession{}
	pool := &guacamolev1alpha1.DesktopPool{}
	sec, enabled := effectiveIdleSeconds(session, pool)
	if enabled || sec != 0 {
		t.Fatalf("idle should be disabled without sessionLifecycle, got sec=%d enabled=%v", sec, enabled)
	}
}

func TestEffectiveIdleSessionOverrideZeroDisables(t *testing.T) {
	session := &guacamolev1alpha1.DesktopSession{
		Spec: guacamolev1alpha1.DesktopSessionSpec{
			IdleSecondsAfterDisconnect: int64Ptr(0),
		},
	}
	pool := &guacamolev1alpha1.DesktopPool{
		Spec: guacamolev1alpha1.DesktopPoolSpec{
			SessionLifecycle: &guacamolev1alpha1.DesktopPoolSessionLifecycleSpec{
				IdleSecondsAfterDisconnect: int64Ptr(900),
			},
		},
	}
	sec, enabled := effectiveIdleSeconds(session, pool)
	if enabled || sec != 0 {
		t.Fatalf("session override 0 should disable idle, got sec=%d enabled=%v", sec, enabled)
	}
}

func TestEffectiveMaxTTLFromPool(t *testing.T) {
	session := &guacamolev1alpha1.DesktopSession{}
	pool := &guacamolev1alpha1.DesktopPool{
		Spec: guacamolev1alpha1.DesktopPoolSpec{
			SessionLifecycle: &guacamolev1alpha1.DesktopPoolSessionLifecycleSpec{
				MaxSecondsAfterReady: int64Ptr(1800),
			},
		},
	}
	sec, enabled := effectiveMaxTTLSeconds(session, pool)
	if !enabled || sec != 1800 {
		t.Fatalf("got sec=%d enabled=%v", sec, enabled)
	}
}
