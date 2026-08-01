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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

const (
	kubeVirtRunStrategyAlways = "Always"
	kubeVirtRunStrategyHalted = "Halted"

	defaultPowerIdleSeconds int64 = 900
)

type powerPlan struct {
	toStop []string
	toWake []string
	// nextIdle is when the soonest Available desktop becomes idle-eligible.
	nextIdle time.Duration
}

func powerManagementEnabled(pool *guacamolev1alpha1.DesktopPool) bool {
	pm := pool.Spec.PowerManagement
	if pm == nil {
		// Default enabled=true when the nested object is present via CRD defaults,
		// but omit entirely means treat as enabled with defaults for new samples.
		// Spec omission: keep backward-compatible warm pool (disabled).
		return false
	}
	if pm.Enabled == nil {
		return true
	}
	return *pm.Enabled
}

func powerIdleSeconds(pool *guacamolev1alpha1.DesktopPool) int64 {
	pm := pool.Spec.PowerManagement
	if pm == nil || pm.IdleSeconds == nil {
		return defaultPowerIdleSeconds
	}
	if *pm.IdleSeconds < 0 {
		return defaultPowerIdleSeconds
	}
	return *pm.IdleSeconds
}

func powerMinReady(pool *guacamolev1alpha1.DesktopPool) int32 {
	if pool.Spec.MinReady == nil {
		return 0
	}
	if *pool.Spec.MinReady < 0 {
		return 0
	}
	return *pool.Spec.MinReady
}

func vmRunStrategy(vm *unstructured.Unstructured) string {
	s, _, _ := unstructured.NestedString(vm.Object, "spec", "runStrategy")
	return s
}

func availableSince(vm *unstructured.Unstructured) (time.Time, bool) {
	ann := vm.GetAnnotations()
	if ann == nil {
		return time.Time{}, false
	}
	raw := ann[guacamolev1alpha1.DesktopAnnotationAvailableSince]
	if raw == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func setAvailableSinceAnnotation(vm *unstructured.Unstructured, when time.Time) {
	ann := vm.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	ann[guacamolev1alpha1.DesktopAnnotationAvailableSince] = when.UTC().Format(time.RFC3339)
	vm.SetAnnotations(ann)
}

func clearAvailableSinceAnnotation(vm *unstructured.Unstructured) {
	ann := vm.GetAnnotations()
	if ann == nil {
		return
	}
	if _, ok := ann[guacamolev1alpha1.DesktopAnnotationAvailableSince]; !ok {
		return
	}
	delete(ann, guacamolev1alpha1.DesktopAnnotationAvailableSince)
	vm.SetAnnotations(ann)
}

// planPowerActions decides which Available desktops to stop and which Stopped to wake.
func planPowerActions(
	now time.Time,
	idleSeconds int64,
	minReady int32,
	available []unstructured.Unstructured,
	stopped []unstructured.Unstructured,
	waiters int,
	forceSuspend bool,
	forceWake bool,
) powerPlan {
	plan := powerPlan{nextIdle: 0}
	idleDur := time.Duration(idleSeconds) * time.Second

	// Sort for deterministic picks.
	sort.SliceStable(available, func(i, j int) bool {
		return available[i].GetName() < available[j].GetName()
	})
	sort.SliceStable(stopped, func(i, j int) bool {
		return stopped[i].GetName() < stopped[j].GetName()
	})

	availableCount := int32(len(available))
	stoppedCount := int32(len(stopped))

	// Idle / forced suspend: never drop below minReady Available.
	for i := range available {
		if availableCount-int32(len(plan.toStop)) <= minReady {
			break
		}
		vm := &available[i]
		since, ok := availableSince(vm)
		if forceSuspend {
			plan.toStop = append(plan.toStop, vm.GetName())
			continue
		}
		if !ok {
			// Missing annotation: treat as just became available; set clock next reconcile.
			continue
		}
		age := now.Sub(since)
		if age >= idleDur {
			plan.toStop = append(plan.toStop, vm.GetName())
			continue
		}
		remaining := idleDur - age
		if plan.nextIdle == 0 || remaining < plan.nextIdle {
			plan.nextIdle = remaining
		}
	}

	// After planned stops, how many Available remain (optimistic).
	remainingAvailable := availableCount - int32(len(plan.toStop))
	if remainingAvailable < 0 {
		remainingAvailable = 0
	}

	needWake := int32(0)
	if forceWake {
		needWake = stoppedCount
	}
	// Wake to serve waiters that exceed remaining Available.
	if int32(waiters) > remainingAvailable {
		deficit := int32(waiters) - remainingAvailable
		if deficit > needWake {
			needWake = deficit
		}
	}
	// Wake to restore minReady warm floor.
	if remainingAvailable < minReady {
		floorDeficit := minReady - remainingAvailable
		if floorDeficit > needWake {
			needWake = floorDeficit
		}
	}
	if needWake > stoppedCount {
		needWake = stoppedCount
	}
	for i := int32(0); i < needWake && i < int32(len(stopped)); i++ {
		plan.toWake = append(plan.toWake, stopped[i].GetName())
	}

	return plan
}

func (r *DesktopPoolReconciler) reconcilePowerManagement(
	ctx context.Context,
	pool *guacamolev1alpha1.DesktopPool,
	vms []unstructured.Unstructured,
) (time.Duration, error) {
	enabled := powerManagementEnabled(pool)

	var available, stopped []unstructured.Unstructured
	for i := range vms {
		vm := &vms[i]
		state := desktopStateFromLabels(vm)
		switch state {
		case guacamolev1alpha1.DesktopStateAvailable:
			available = append(available, *vm)
		case guacamolev1alpha1.DesktopStateStopped:
			stopped = append(stopped, *vm)
		}
	}

	if !enabled {
		// Wake any leftover Stopped VMs so the pool returns to always-on.
		for i := range stopped {
			if err := r.wakeDesktopVM(ctx, &stopped[i]); err != nil {
				return 0, err
			}
		}
		if err := r.clearPowerRequest(ctx, pool); err != nil {
			return 0, err
		}
		return 0, nil
	}

	waiters, err := r.countPoolWaiters(ctx, pool.Namespace, pool.Name)
	if err != nil {
		return 0, err
	}

	req := ""
	if ann := pool.GetAnnotations(); ann != nil {
		req = ann[guacamolev1alpha1.DesktopAnnotationPowerRequest]
	}
	forceWake := req == guacamolev1alpha1.DesktopPowerRequestWake
	forceSuspend := req == guacamolev1alpha1.DesktopPowerRequestSuspend

	plan := planPowerActions(
		time.Now(),
		powerIdleSeconds(pool),
		powerMinReady(pool),
		available,
		stopped,
		waiters,
		forceSuspend,
		forceWake,
	)

	byName := map[string]*unstructured.Unstructured{}
	for i := range vms {
		byName[vms[i].GetName()] = &vms[i]
	}

	for _, name := range plan.toStop {
		vm := byName[name]
		if vm == nil {
			continue
		}
		if err := r.stopDesktopVM(ctx, vm); err != nil {
			return 0, err
		}
	}
	for _, name := range plan.toWake {
		vm := byName[name]
		if vm == nil {
			continue
		}
		if err := r.wakeDesktopVM(ctx, vm); err != nil {
			return 0, err
		}
	}

	if req != "" {
		if err := r.clearPowerRequest(ctx, pool); err != nil {
			return 0, err
		}
	}

	return plan.nextIdle, nil
}

func (r *DesktopPoolReconciler) countPoolWaiters(ctx context.Context, namespace, poolName string) (int, error) {
	list := &guacamolev1alpha1.DesktopSessionList{}
	if err := r.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return 0, err
	}
	n := 0
	for i := range list.Items {
		s := &list.Items[i]
		if s.Spec.PoolRef.Name != poolName {
			continue
		}
		if isWaitingForBroker(s) {
			n++
		}
	}
	return n, nil
}

func (r *DesktopPoolReconciler) stopDesktopVM(ctx context.Context, vm *unstructured.Unstructured) error {
	fresh := &unstructured.Unstructured{}
	fresh.SetGroupVersionKind(virtualMachineGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: vm.GetName(), Namespace: vm.GetNamespace()}, fresh); err != nil {
		return err
	}
	labels := fresh.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	// Never stop allocated desktops.
	if labels[guacamolev1alpha1.DesktopLabelState] == string(guacamolev1alpha1.DesktopStateAllocated) ||
		labels[guacamolev1alpha1.DesktopLabelState] == string(guacamolev1alpha1.DesktopStateInUse) {
		return nil
	}
	if err := unstructured.SetNestedField(fresh.Object, kubeVirtRunStrategyHalted, "spec", "runStrategy"); err != nil {
		return err
	}
	labels[guacamolev1alpha1.DesktopLabelState] = string(guacamolev1alpha1.DesktopStateStopped)
	fresh.SetLabels(labels)
	clearAvailableSinceAnnotation(fresh)
	return r.Update(ctx, fresh)
}

func (r *DesktopPoolReconciler) wakeDesktopVM(ctx context.Context, vm *unstructured.Unstructured) error {
	fresh := &unstructured.Unstructured{}
	fresh.SetGroupVersionKind(virtualMachineGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: vm.GetName(), Namespace: vm.GetNamespace()}, fresh); err != nil {
		return err
	}
	if err := unstructured.SetNestedField(fresh.Object, kubeVirtRunStrategyAlways, "spec", "runStrategy"); err != nil {
		return err
	}
	labels := fresh.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	// Only transition Stopped → Booting; leave Allocated alone.
	if labels[guacamolev1alpha1.DesktopLabelState] == string(guacamolev1alpha1.DesktopStateStopped) ||
		vmRunStrategy(fresh) == kubeVirtRunStrategyHalted {
		labels[guacamolev1alpha1.DesktopLabelState] = string(guacamolev1alpha1.DesktopStateBooting)
	}
	fresh.SetLabels(labels)
	clearAvailableSinceAnnotation(fresh)
	return r.Update(ctx, fresh)
}

func (r *DesktopPoolReconciler) clearPowerRequest(ctx context.Context, pool *guacamolev1alpha1.DesktopPool) error {
	latest := &guacamolev1alpha1.DesktopPool{}
	if err := r.Get(ctx, types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}, latest); err != nil {
		return err
	}
	ann := latest.GetAnnotations()
	if ann == nil {
		return nil
	}
	if _, ok := ann[guacamolev1alpha1.DesktopAnnotationPowerRequest]; !ok {
		return nil
	}
	delete(ann, guacamolev1alpha1.DesktopAnnotationPowerRequest)
	latest.SetAnnotations(ann)
	if err := r.Update(ctx, latest); err != nil {
		return err
	}
	pool.SetAnnotations(latest.GetAnnotations())
	return nil
}

// markDesktopAvailable sets state=Available and starts the idle clock in one Update.
func (r *DesktopPoolReconciler) markDesktopAvailable(ctx context.Context, vm *unstructured.Unstructured) error {
	fresh := &unstructured.Unstructured{}
	fresh.SetGroupVersionKind(virtualMachineGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: vm.GetName(), Namespace: vm.GetNamespace()}, fresh); err != nil {
		return err
	}
	labels := fresh.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	labels[guacamolev1alpha1.DesktopLabelState] = string(guacamolev1alpha1.DesktopStateAvailable)
	fresh.SetLabels(labels)
	setAvailableSinceAnnotation(fresh, time.Now())
	if err := r.Update(ctx, fresh); err != nil {
		return err
	}
	vm.SetLabels(fresh.GetLabels())
	vm.SetAnnotations(fresh.GetAnnotations())
	return nil
}

func (r *DesktopPoolReconciler) ensureAvailableSince(ctx context.Context, vm *unstructured.Unstructured) error {
	if _, ok := availableSince(vm); ok {
		return nil
	}
	fresh := &unstructured.Unstructured{}
	fresh.SetGroupVersionKind(virtualMachineGVK)
	if err := r.Get(ctx, types.NamespacedName{Name: vm.GetName(), Namespace: vm.GetNamespace()}, fresh); err != nil {
		return err
	}
	if desktopStateFromLabels(fresh) != guacamolev1alpha1.DesktopStateAvailable {
		return nil
	}
	if _, ok := availableSince(fresh); ok {
		return nil
	}
	setAvailableSinceAnnotation(fresh, time.Now())
	if err := r.Update(ctx, fresh); err != nil {
		return err
	}
	vm.SetAnnotations(fresh.GetAnnotations())
	return nil
}

// mapSessionToPool enqueues the DesktopPool when a session for that pool changes,
// so wake-on-demand reacts quickly to new waiters.
func (r *DesktopPoolReconciler) mapSessionToPool(_ context.Context, obj client.Object) []reconcile.Request {
	session, ok := obj.(*guacamolev1alpha1.DesktopSession)
	if !ok || session.Spec.PoolRef.Name == "" {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{
			Name:      session.Spec.PoolRef.Name,
			Namespace: session.Namespace,
		},
	}}
}

func powerManagementConditionMessage(pool *guacamolev1alpha1.DesktopPool, enabled bool) string {
	if !enabled {
		return "power management disabled; desktops stay running"
	}
	return fmt.Sprintf("idle stop after %ds; minReady=%d", powerIdleSeconds(pool), powerMinReady(pool))
}

// isVMIntentionallyStopped reports whether power management has halted this VM.
func isVMIntentionallyStopped(vm *unstructured.Unstructured) bool {
	state := desktopStateFromLabels(vm)
	if state == guacamolev1alpha1.DesktopStateStopped {
		return true
	}
	return vmRunStrategy(vm) == kubeVirtRunStrategyHalted &&
		state != guacamolev1alpha1.DesktopStateAllocated &&
		state != guacamolev1alpha1.DesktopStateInUse
}

// syncMemberStatesFromVMs refreshes member states from live VM labels after power actions.
func syncMemberStatesFromVMs(
	vms []unstructured.Unstructured,
	previous []guacamolev1alpha1.DesktopMemberStatus,
) []guacamolev1alpha1.DesktopMemberStatus {
	prevByName := make(map[string]guacamolev1alpha1.DesktopMemberStatus, len(previous))
	for _, m := range previous {
		prevByName[m.Name] = m
	}
	out := make([]guacamolev1alpha1.DesktopMemberStatus, 0, len(vms))
	for i := range vms {
		vm := &vms[i]
		state := desktopStateFromLabels(vm)
		member := guacamolev1alpha1.DesktopMemberStatus{
			Name:  vm.GetName(),
			State: state,
		}
		if prev, ok := prevByName[vm.GetName()]; ok {
			member.ServiceDNS = prev.ServiceDNS
			member.ConnectionName = prev.ConnectionName
			member.Message = prev.Message
		}
		switch state {
		case guacamolev1alpha1.DesktopStateStopped:
			member.Message = "powered-off"
			member.ConnectionName = ""
		case guacamolev1alpha1.DesktopStateBooting:
			if member.Message == "powered-off" || member.Message == "" {
				member.Message = "waking"
			}
		case guacamolev1alpha1.DesktopStateAvailable:
			member.Message = ""
		}
		if member.ServiceDNS == "" {
			// Namespace unknown here; leave empty — previous pass usually filled it.
		}
		out = append(out, member)
	}
	return out
}
