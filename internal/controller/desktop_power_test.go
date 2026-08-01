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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

func testVM(name, state string, availableSince time.Time) unstructured.Unstructured {
	vm := unstructured.Unstructured{}
	vm.SetName(name)
	vm.SetLabels(map[string]string{
		guacamolev1alpha1.DesktopLabelState: state,
	})
	if !availableSince.IsZero() {
		vm.SetAnnotations(map[string]string{
			guacamolev1alpha1.DesktopAnnotationAvailableSince: availableSince.UTC().Format(time.RFC3339),
		})
	}
	return vm
}

func TestPlanPowerActionsIdleStop(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	available := []unstructured.Unstructured{
		testVM("a", string(guacamolev1alpha1.DesktopStateAvailable), now.Add(-20*time.Minute)),
		testVM("b", string(guacamolev1alpha1.DesktopStateAvailable), now.Add(-5*time.Minute)),
	}
	plan := planPowerActions(now, 900, 0, available, nil, 0, false, false)
	if len(plan.toStop) != 1 || plan.toStop[0] != "a" {
		t.Fatalf("expected stop [a], got %#v", plan.toStop)
	}
	if plan.nextIdle <= 0 || plan.nextIdle > 10*time.Minute {
		t.Fatalf("expected nextIdle for b around 10m, got %v", plan.nextIdle)
	}
}

func TestPlanPowerActionsRespectsMinReady(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	available := []unstructured.Unstructured{
		testVM("a", string(guacamolev1alpha1.DesktopStateAvailable), now.Add(-30*time.Minute)),
		testVM("b", string(guacamolev1alpha1.DesktopStateAvailable), now.Add(-30*time.Minute)),
	}
	plan := planPowerActions(now, 900, 1, available, nil, 0, false, false)
	if len(plan.toStop) != 1 {
		t.Fatalf("expected exactly 1 stop with minReady=1, got %#v", plan.toStop)
	}
}

func TestPlanPowerActionsWakeForWaiters(t *testing.T) {
	now := time.Now()
	stopped := []unstructured.Unstructured{
		testVM("s1", string(guacamolev1alpha1.DesktopStateStopped), time.Time{}),
		testVM("s2", string(guacamolev1alpha1.DesktopStateStopped), time.Time{}),
	}
	plan := planPowerActions(now, 900, 0, nil, stopped, 2, false, false)
	if len(plan.toWake) != 2 {
		t.Fatalf("expected wake 2, got %#v", plan.toWake)
	}
}

func TestPlanPowerActionsWakeMinReady(t *testing.T) {
	now := time.Now()
	stopped := []unstructured.Unstructured{
		testVM("s1", string(guacamolev1alpha1.DesktopStateStopped), time.Time{}),
		testVM("s2", string(guacamolev1alpha1.DesktopStateStopped), time.Time{}),
	}
	plan := planPowerActions(now, 900, 1, nil, stopped, 0, false, false)
	if len(plan.toWake) != 1 || plan.toWake[0] != "s1" {
		t.Fatalf("expected wake [s1] for minReady, got %#v", plan.toWake)
	}
}

func TestPlanPowerActionsForceSuspendAndWake(t *testing.T) {
	now := time.Now()
	available := []unstructured.Unstructured{
		testVM("a", string(guacamolev1alpha1.DesktopStateAvailable), now),
	}
	stopped := []unstructured.Unstructured{
		testVM("s1", string(guacamolev1alpha1.DesktopStateStopped), time.Time{}),
	}
	suspend := planPowerActions(now, 900, 0, available, stopped, 0, true, false)
	if len(suspend.toStop) != 1 || suspend.toStop[0] != "a" {
		t.Fatalf("force suspend: %#v", suspend.toStop)
	}
	wake := planPowerActions(now, 900, 0, nil, stopped, 0, false, true)
	if len(wake.toWake) != 1 || wake.toWake[0] != "s1" {
		t.Fatalf("force wake: %#v", wake.toWake)
	}
}

func TestScaleDownPriorityStoppedFirst(t *testing.T) {
	if scaleDownPriority(guacamolev1alpha1.DesktopStateStopped) >= scaleDownPriority(guacamolev1alpha1.DesktopStateAvailable) {
		t.Fatalf("Stopped should scale down before Available")
	}
	if scaleDownPriority(guacamolev1alpha1.DesktopStateFailed) >= scaleDownPriority(guacamolev1alpha1.DesktopStateStopped) {
		t.Fatalf("Failed should scale down before Stopped")
	}
}

func TestSummarizeMembersCountsStopped(t *testing.T) {
	counts := summarizeMembers([]guacamolev1alpha1.DesktopMemberStatus{
		{State: guacamolev1alpha1.DesktopStateAvailable},
		{State: guacamolev1alpha1.DesktopStateStopped},
		{State: guacamolev1alpha1.DesktopStateStopped},
		{State: guacamolev1alpha1.DesktopStateAllocated},
		{State: guacamolev1alpha1.DesktopStateBooting},
	})
	if counts.available != 1 || counts.stopped != 2 || counts.allocated != 1 || counts.provisioning != 1 {
		t.Fatalf("unexpected counts: %+v", counts)
	}
}

func TestPowerManagementEnabledDefaults(t *testing.T) {
	pool := &guacamolev1alpha1.DesktopPool{}
	if powerManagementEnabled(pool) {
		t.Fatal("omitted powerManagement should be disabled")
	}
	enabled := true
	pool.Spec.PowerManagement = &guacamolev1alpha1.DesktopPoolPowerManagementSpec{}
	if !powerManagementEnabled(pool) {
		t.Fatal("empty powerManagement object should default enabled=true")
	}
	pool.Spec.PowerManagement.Enabled = &enabled
	if !powerManagementEnabled(pool) {
		t.Fatal("explicit enabled=true")
	}
	disabled := false
	pool.Spec.PowerManagement.Enabled = &disabled
	if powerManagementEnabled(pool) {
		t.Fatal("explicit enabled=false")
	}
	if powerIdleSeconds(pool) != 900 {
		t.Fatalf("default idleSeconds want 900 got %d", powerIdleSeconds(pool))
	}
}
