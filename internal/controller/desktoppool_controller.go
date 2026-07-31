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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
	"github.com/raphaelmorsch/guacamole-operator/internal/readiness"
)

const (
	desktopPoolFinalizer = "guacamole.guacamole.io/desktoppool-finalizer"

	virtualMachineAPIVersion = "kubevirt.io/v1"
	virtualMachineKind       = "VirtualMachine"
	virtualMachineInstanceKind = "VirtualMachineInstance"
)

var (
	virtualMachineGVK = schema.GroupVersionKind{
		Group:   "kubevirt.io",
		Version: "v1",
		Kind:    virtualMachineKind,
	}
	virtualMachineInstanceGVK = schema.GroupVersionKind{
		Group:   "kubevirt.io",
		Version: "v1",
		Kind:    virtualMachineInstanceKind,
	}
)

// DesktopPoolReconciler reconciles a DesktopPool object.
type DesktopPoolReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	Prober readiness.DesktopReadinessProber
}

// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktoppools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktoppools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=desktoppools/finalizers,verbs=update
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=guacamoleconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=guacamole.guacamole.io,resources=guacamoles,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services;endpoints;secrets;serviceaccounts;pods;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines;virtualmachineinstances,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cdi.kubevirt.io,resources=datavolumes;datasources,verbs=get;list;watch;create;update;patch;delete
// Pods create is required so the operator can grant CDI cross-namespace clone RBAC
// (Kubernetes forbids escalating privileges the SA does not already hold).

func (r *DesktopPoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pool := &guacamolev1alpha1.DesktopPool{}
	if err := r.Get(ctx, req.NamespacedName, pool); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !pool.DeletionTimestamp.IsZero() {
		return r.finalizePool(ctx, pool)
	}

	if !controllerutil.ContainsFinalizer(pool, desktopPoolFinalizer) {
		controllerutil.AddFinalizer(pool, desktopPoolFinalizer)
		if err := r.Update(ctx, pool); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if err := validateDesktopPoolSpec(pool); err != nil {
		logger.Error(err, "invalid DesktopPool spec")
		_ = r.patchStatus(ctx, pool, func(status *guacamolev1alpha1.DesktopPoolStatus) {
			status.Phase = guacamolev1alpha1.DesktopPoolPhaseFailed
			setDesktopPoolCondition(status, "Ready", metav1.ConditionFalse, "InvalidSpec", err.Error())
		})
		return ctrl.Result{}, err
	}

	if err := r.ensureCloneRBAC(ctx, pool); err != nil {
		logger.Error(err, "failed to ensure CDI clone RBAC in golden-image namespace",
			"dataSourceNamespace", dataSourceNamespace(pool))
		_ = r.patchStatus(ctx, pool, func(status *guacamolev1alpha1.DesktopPoolStatus) {
			status.Phase = guacamolev1alpha1.DesktopPoolPhaseFailed
			status.DataSourceNamespace = dataSourceNamespace(pool)
			setDesktopPoolCondition(status, "CloneAuthorized", metav1.ConditionFalse, "CloneRBACFailed", err.Error())
			setDesktopPoolCondition(status, "Ready", metav1.ConditionFalse, "CloneRBACFailed", err.Error())
		})
		return ctrl.Result{RequeueAfter: 30 * time.Second}, err
	}
	_ = r.patchStatus(ctx, pool, func(status *guacamolev1alpha1.DesktopPoolStatus) {
		status.DataSourceNamespace = dataSourceNamespace(pool)
		setDesktopPoolCondition(status, "CloneAuthorized", metav1.ConditionTrue, "CloneRBACReady",
			fmt.Sprintf("clone RBAC ready in namespace %s", dataSourceNamespace(pool)))
	})

	vms, err := r.listPoolVMs(ctx, pool)
	if err != nil {
		return ctrl.Result{}, err
	}

	members := make([]guacamolev1alpha1.DesktopMemberStatus, 0, len(vms))
	for i := range vms {
		member, err := r.reconcileDesktopMember(ctx, pool, &vms[i])
		if err != nil {
			logger.Error(err, "failed to reconcile desktop member", "vm", vms[i].GetName())
			member = guacamolev1alpha1.DesktopMemberStatus{
				Name:    vms[i].GetName(),
				State:   guacamolev1alpha1.DesktopStateFailed,
				Message: err.Error(),
			}
			_ = r.setVMStateLabel(ctx, &vms[i], guacamolev1alpha1.DesktopStateFailed)
		}
		members = append(members, member)
	}

	desired := desktopPoolDesiredReplicas(pool)
	current := int32(len(members))

	switch {
	case current < desired:
		toCreate := desired - current
		for i := int32(0); i < toCreate; i++ {
			if err := r.createDesktopVM(ctx, pool); err != nil {
				logger.Error(err, "failed to create desktop VM")
				_ = r.patchStatus(ctx, pool, func(status *guacamolev1alpha1.DesktopPoolStatus) {
					status.Phase = guacamolev1alpha1.DesktopPoolPhaseFailed
					setDesktopPoolCondition(status, "Ready", metav1.ConditionFalse, "CreateFailed", err.Error())
				})
				return ctrl.Result{RequeueAfter: 30 * time.Second}, err
			}
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	case current > desired:
		if err := r.scaleDown(ctx, pool, members, current-desired); err != nil {
			return ctrl.Result{RequeueAfter: 15 * time.Second}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	var credentialsSecretName string
	if poolCreatesConnections(pool) {
		credRef, err := r.ensureCredentialsSecret(ctx, pool)
		if err != nil {
			logger.Error(err, "failed to ensure Windows RDP credentials secret")
			_ = r.patchStatus(ctx, pool, func(status *guacamolev1alpha1.DesktopPoolStatus) {
				status.Phase = guacamolev1alpha1.DesktopPoolPhaseFailed
				setDesktopPoolCondition(status, "CredentialsReady", metav1.ConditionFalse, "CredentialsFailed", err.Error())
				setDesktopPoolCondition(status, "Ready", metav1.ConditionFalse, "CredentialsFailed", err.Error())
			})
			return ctrl.Result{RequeueAfter: 30 * time.Second}, err
		}
		credentialsSecretName = credRef.Name
		_ = r.patchStatus(ctx, pool, func(status *guacamolev1alpha1.DesktopPoolStatus) {
			status.CredentialsSecret = credentialsSecretName
			setDesktopPoolCondition(status, "CredentialsReady", metav1.ConditionTrue, "CredentialsReady",
				fmt.Sprintf("using secret %s/%s key=%s", pool.Namespace, credRef.Name, credRef.Key))
		})

		for i := range members {
			if members[i].State != guacamolev1alpha1.DesktopStateAvailable {
				continue
			}
			connName, err := r.ensureGuacamoleConnection(ctx, pool, members[i], credRef)
			if err != nil {
				logger.Error(err, "failed to ensure GuacamoleConnection", "vm", members[i].Name)
				members[i].Message = err.Error()
				continue
			}
			members[i].ConnectionName = connName
		}
	}

	statusCounts := summarizeMembers(members)
	phase := guacamolev1alpha1.DesktopPoolPhaseReady
	reason := "ReplicasReady"
	message := fmt.Sprintf("desired=%d available=%d", desired, statusCounts.available)
	if statusCounts.provisioning > 0 || statusCounts.failed > 0 {
		phase = guacamolev1alpha1.DesktopPoolPhaseScaling
		reason = "Provisioning"
		message = fmt.Sprintf("desired=%d provisioning=%d available=%d failed=%d",
			desired, statusCounts.provisioning, statusCounts.available, statusCounts.failed)
	}
	if desired == 0 {
		phase = guacamolev1alpha1.DesktopPoolPhasePending
		reason = "ZeroReplicas"
		message = "replicas is 0"
	}

	if err := r.patchStatus(ctx, pool, func(status *guacamolev1alpha1.DesktopPoolStatus) {
		status.Phase = phase
		status.Desired = desired
		status.Provisioning = statusCounts.provisioning
		status.Available = statusCounts.available
		status.Allocated = statusCounts.allocated
		status.Failed = statusCounts.failed
		status.DataSourceNamespace = dataSourceNamespace(pool)
		if credentialsSecretName != "" {
			status.CredentialsSecret = credentialsSecretName
		}
		status.Desktops = members
		ready := phase == guacamolev1alpha1.DesktopPoolPhaseReady
		condStatus := metav1.ConditionFalse
		if ready {
			condStatus = metav1.ConditionTrue
		}
		setDesktopPoolCondition(status, "CloneAuthorized", metav1.ConditionTrue, "CloneRBACReady",
			fmt.Sprintf("clone RBAC ready in namespace %s", dataSourceNamespace(pool)))
		setDesktopPoolCondition(status, "Ready", condStatus, reason, message)
	}); err != nil {
		return ctrl.Result{}, err
	}

	requeueAfter := 2 * time.Minute
	if statusCounts.provisioning > 0 || statusCounts.failed > 0 {
		requeueAfter = 20 * time.Second
	}
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

func (r *DesktopPoolReconciler) reconcileDesktopMember(
	ctx context.Context,
	pool *guacamolev1alpha1.DesktopPool,
	vm *unstructured.Unstructured,
) (guacamolev1alpha1.DesktopMemberStatus, error) {
	name := vm.GetName()
	state := desktopStateFromLabels(vm)
	member := guacamolev1alpha1.DesktopMemberStatus{
		Name:       name,
		State:      state,
		ServiceDNS: rdpServiceDNS(name, pool.Namespace),
	}

	if state == guacamolev1alpha1.DesktopStateAllocated ||
		state == guacamolev1alpha1.DesktopStateInUse ||
		state == guacamolev1alpha1.DesktopStateReleasing ||
		state == guacamolev1alpha1.DesktopStateDeleting {
		return member, nil
	}

	if err := r.ensureRDPService(ctx, pool, name); err != nil {
		return member, err
	}

	ready, detail, err := r.assessDesktopReadiness(ctx, pool, name)
	if err != nil {
		_ = r.setVMStateLabel(ctx, vm, guacamolev1alpha1.DesktopStateFailed)
		member.State = guacamolev1alpha1.DesktopStateFailed
		member.Message = err.Error()
		return member, nil
	}
	if !ready {
		next := guacamolev1alpha1.DesktopStateBooting
		if detail == "waiting-for-datavolume" || detail == "waiting-for-vm" {
			next = guacamolev1alpha1.DesktopStateProvisioning
		}
		_ = r.setVMStateLabel(ctx, vm, next)
		member.State = next
		member.Message = detail
		return member, nil
	}

	_ = r.setVMStateLabel(ctx, vm, guacamolev1alpha1.DesktopStateAvailable)
	member.State = guacamolev1alpha1.DesktopStateAvailable
	member.Message = ""
	return member, nil
}

func (r *DesktopPoolReconciler) assessDesktopReadiness(
	ctx context.Context,
	pool *guacamolev1alpha1.DesktopPool,
	vmName string,
) (bool, string, error) {
	vmi := &unstructured.Unstructured{}
	vmi.SetGroupVersionKind(virtualMachineInstanceGVK)
	err := r.Get(ctx, types.NamespacedName{Name: vmName, Namespace: pool.Namespace}, vmi)
	if apierrors.IsNotFound(err) {
		return false, "waiting-for-vm", nil
	}
	if err != nil {
		return false, "", err
	}

	phase, _, _ := unstructured.NestedString(vmi.Object, "status", "phase")
	if phase != "Running" {
		return false, fmt.Sprintf("vmi-phase-%s", phase), nil
	}

	readyCond := unstructuredConditionStatus(vmi, "Ready")
	if readyCond != "True" {
		return false, "vmi-not-ready", nil
	}

	svcName := rdpServiceName(vmName)
	eps := &corev1.Endpoints{}
	if err := r.Get(ctx, types.NamespacedName{Name: svcName, Namespace: pool.Namespace}, eps); err != nil {
		if apierrors.IsNotFound(err) {
			return false, "waiting-for-endpoints", nil
		}
		return false, "", err
	}
	if !endpointsHaveAddresses(eps) {
		return false, "waiting-for-endpoints", nil
	}

	port := desktopRDPPort(pool)
	host := fmt.Sprintf("%s.%s.svc", svcName, pool.Namespace)
	if err := r.prober().Probe(ctx, host, port); err != nil {
		return false, "waiting-for-rdp", nil
	}
	return true, "", nil
}

func (r *DesktopPoolReconciler) createDesktopVM(ctx context.Context, pool *guacamolev1alpha1.DesktopPool) error {
	suffix, err := randomSuffix(4)
	if err != nil {
		return err
	}
	vmName := fmt.Sprintf("%s-%s", pool.Name, suffix)
	diskName := fmt.Sprintf("%s-root", vmName)

	vm := buildDesktopVirtualMachine(pool, vmName, diskName)
	if err := controllerutil.SetControllerReference(pool, vm, r.Scheme); err != nil {
		return err
	}
	if err := r.Create(ctx, vm); err != nil {
		return err
	}
	return r.ensureRDPService(ctx, pool, vmName)
}

func buildDesktopVirtualMachine(pool *guacamolev1alpha1.DesktopPool, vmName, diskName string) *unstructured.Unstructured {
	dsNamespace := pool.Spec.Source.DataSource.Namespace
	if dsNamespace == "" {
		dsNamespace = pool.Namespace
	}
	diskSize := pool.Spec.VirtualMachine.DiskSize
	if diskSize == "" {
		diskSize = "40Gi"
	}
	cpu := int64(2)
	if pool.Spec.VirtualMachine.CPU != nil {
		cpu = int64(*pool.Spec.VirtualMachine.CPU)
	}
	memory := pool.Spec.VirtualMachine.Memory
	if memory == "" {
		memory = "4Gi"
	}

	labels := map[string]interface{}{
		guacamolev1alpha1.DesktopLabelPool:      pool.Name,
		guacamolev1alpha1.DesktopLabelState:     string(guacamolev1alpha1.DesktopStateProvisioning),
		guacamolev1alpha1.DesktopLabelManagedBy: guacamolev1alpha1.DesktopManagedByValue,
		"kubevirt.io/domain":                    vmName,
	}

	templateLabels := map[string]interface{}{
		guacamolev1alpha1.DesktopLabelPool: pool.Name,
		"kubevirt.io/domain":               vmName,
	}

	dvTemplate := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": diskName,
			"labels": map[string]interface{}{
				guacamolev1alpha1.DesktopLabelPool: pool.Name,
				guacamolev1alpha1.DesktopLabelVM:   vmName,
			},
			"annotations": map[string]interface{}{
				"cdi.kubevirt.io/storage.bind.immediate.requested": "true",
			},
		},
		"spec": map[string]interface{}{
			"sourceRef": map[string]interface{}{
				"kind":      "DataSource",
				"name":      pool.Spec.Source.DataSource.Name,
				"namespace": dsNamespace,
			},
			"storage": map[string]interface{}{
				"storageClassName": pool.Spec.VirtualMachine.StorageClassName,
				"accessModes":      []interface{}{"ReadWriteOnce"},
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{
						"storage": diskSize,
					},
				},
			},
		},
	}

	domain := map[string]interface{}{
		"firmware": map[string]interface{}{
			"bootloader": map[string]interface{}{
				"bios": map[string]interface{}{},
			},
		},
		"devices": map[string]interface{}{
			"disks": []interface{}{
				map[string]interface{}{
					"name": "rootdisk",
					"disk": map[string]interface{}{"bus": "virtio"},
				},
			},
			"interfaces": []interface{}{
				map[string]interface{}{
					"name":       "default",
					"masquerade": map[string]interface{}{},
				},
			},
		},
	}

	spec := map[string]interface{}{
		"runStrategy": "Always",
		"dataVolumeTemplates": []interface{}{dvTemplate},
		"template": map[string]interface{}{
			"metadata": map[string]interface{}{
				"labels": templateLabels,
			},
			"spec": map[string]interface{}{
				"domain": domain,
				"serviceAccountName": desktopPoolCloneServiceAccount,
				"networks": []interface{}{
					map[string]interface{}{
						"name": "default",
						"pod":  map[string]interface{}{},
					},
				},
				"volumes": []interface{}{
					map[string]interface{}{
						"name": "rootdisk",
						"dataVolume": map[string]interface{}{
							"name": diskName,
						},
					},
				},
			},
		},
	}

	if pool.Spec.VirtualMachine.InstanceType != "" {
		spec["instancetype"] = map[string]interface{}{
			"kind": "VirtualMachineClusterInstancetype",
			"name": pool.Spec.VirtualMachine.InstanceType,
		}
		domain["resources"] = map[string]interface{}{}
	} else {
		domain["cpu"] = map[string]interface{}{"cores": cpu}
		domain["resources"] = map[string]interface{}{
			"requests": map[string]interface{}{
				"memory": memory,
			},
		}
	}

	vm := &unstructured.Unstructured{Object: map[string]interface{}{}}
	vm.SetGroupVersionKind(virtualMachineGVK)
	vm.SetName(vmName)
	vm.SetNamespace(pool.Namespace)
	vm.SetLabels(stringifyLabels(labels))
	if err := unstructured.SetNestedMap(vm.Object, spec, "spec"); err != nil {
		// Should not happen with a fresh map; keep construct usable.
		vm.Object["spec"] = spec
	}
	return vm
}

func (r *DesktopPoolReconciler) ensureRDPService(ctx context.Context, pool *guacamolev1alpha1.DesktopPool, vmName string) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rdpServiceName(vmName),
			Namespace: pool.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := controllerutil.SetControllerReference(pool, svc, r.Scheme); err != nil {
			return err
		}
		svc.Labels = map[string]string{
			guacamolev1alpha1.DesktopLabelPool:      pool.Name,
			guacamolev1alpha1.DesktopLabelVM:        vmName,
			guacamolev1alpha1.DesktopLabelManagedBy: guacamolev1alpha1.DesktopManagedByValue,
		}
		port := desktopRDPPort(pool)
		svc.Spec.Selector = map[string]string{
			"kubevirt.io/domain": vmName,
		}
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       "rdp",
			Protocol:   corev1.ProtocolTCP,
			Port:       port,
			TargetPort: intstr.FromInt(int(port)),
		}}
		return nil
	})
	return err
}

func (r *DesktopPoolReconciler) ensureCredentialsSecret(
	ctx context.Context,
	pool *guacamolev1alpha1.DesktopPool,
) (*guacamolev1alpha1.SecretKeyRef, error) {
	return resolveDesktopCredentials(ctx, r.Client, r.Scheme, pool, pool)
}

func (r *DesktopPoolReconciler) ensureGuacamoleConnection(
	ctx context.Context,
	pool *guacamolev1alpha1.DesktopPool,
	member guacamolev1alpha1.DesktopMemberStatus,
	credRef *guacamolev1alpha1.SecretKeyRef,
) (string, error) {
	connName := member.Name
	conn := &guacamolev1alpha1.GuacamoleConnection{
		ObjectMeta: metav1.ObjectMeta{
			Name:      connName,
			Namespace: pool.Namespace,
		},
	}

	ignoreCert := true
	if pool.Spec.Guacamole.IgnoreCert != nil {
		ignoreCert = *pool.Spec.Guacamole.IgnoreCert
	}
	security := pool.Spec.Guacamole.Security
	if security == "" {
		security = "nla"
	}
	username := pool.Spec.Guacamole.Username
	if username == "" {
		username = "Administrator"
	}
	port := desktopRDPPort(pool)
	host := fmt.Sprintf("%s.%s.svc", rdpServiceName(member.Name), pool.Namespace)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, conn, func() error {
		if err := controllerutil.SetControllerReference(pool, conn, r.Scheme); err != nil {
			return err
		}
		if conn.Labels == nil {
			conn.Labels = map[string]string{}
		}
		conn.Labels[guacamolev1alpha1.DesktopLabelPool] = pool.Name
		conn.Labels[guacamolev1alpha1.DesktopLabelVM] = member.Name
		conn.Labels[guacamolev1alpha1.DesktopLabelManagedBy] = guacamolev1alpha1.DesktopManagedByValue

		conn.Spec.GuacamoleRef = pool.Spec.Guacamole.InstanceRef
		conn.Spec.DisplayName = member.Name
		conn.Spec.Protocol = "rdp"
		conn.Spec.ParentGroup = pool.Spec.Guacamole.ParentGroup
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
	return connName, err
}

func (r *DesktopPoolReconciler) scaleDown(
	ctx context.Context,
	pool *guacamolev1alpha1.DesktopPool,
	members []guacamolev1alpha1.DesktopMemberStatus,
	toRemove int32,
) error {
	candidates := make([]guacamolev1alpha1.DesktopMemberStatus, 0, len(members))
	for _, m := range members {
		switch m.State {
		case guacamolev1alpha1.DesktopStateAvailable,
			guacamolev1alpha1.DesktopStateProvisioning,
			guacamolev1alpha1.DesktopStateBooting,
			guacamolev1alpha1.DesktopStateFailed:
			candidates = append(candidates, m)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return scaleDownPriority(candidates[i].State) < scaleDownPriority(candidates[j].State) ||
			(scaleDownPriority(candidates[i].State) == scaleDownPriority(candidates[j].State) &&
				candidates[i].Name < candidates[j].Name)
	})

	removed := int32(0)
	for _, m := range candidates {
		if removed >= toRemove {
			break
		}
		if err := r.deleteDesktop(ctx, pool, m.Name); err != nil {
			return err
		}
		removed++
	}
	if removed < toRemove {
		return fmt.Errorf("unable to scale down: need to remove %d but only %d removable desktops", toRemove, removed)
	}
	return nil
}

func scaleDownPriority(state guacamolev1alpha1.DesktopState) int {
	switch state {
	case guacamolev1alpha1.DesktopStateFailed:
		return 0
	case guacamolev1alpha1.DesktopStateProvisioning:
		return 1
	case guacamolev1alpha1.DesktopStateBooting:
		return 2
	case guacamolev1alpha1.DesktopStateAvailable:
		return 3
	default:
		return 99
	}
}

func (r *DesktopPoolReconciler) deleteDesktop(ctx context.Context, pool *guacamolev1alpha1.DesktopPool, vmName string) error {
	return deleteDesktopResources(ctx, r.Client, pool.Namespace, vmName)
}

func (r *DesktopPoolReconciler) finalizePool(ctx context.Context, pool *guacamolev1alpha1.DesktopPool) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(pool, desktopPoolFinalizer) {
		return ctrl.Result{}, nil
	}

	_ = r.patchStatus(ctx, pool, func(status *guacamolev1alpha1.DesktopPoolStatus) {
		status.Phase = guacamolev1alpha1.DesktopPoolPhaseDeleting
	})

	vms, err := r.listPoolVMs(ctx, pool)
	if err != nil {
		return ctrl.Result{}, err
	}
	for i := range vms {
		if err := r.deleteDesktop(ctx, pool, vms[i].GetName()); err != nil {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, err
		}
	}

	// Catch orphan services/connections labeled for this pool.
	svcList := &corev1.ServiceList{}
	if err := r.List(ctx, svcList, client.InNamespace(pool.Namespace), client.MatchingLabels{
		guacamolev1alpha1.DesktopLabelPool: pool.Name,
	}); err != nil {
		return ctrl.Result{}, err
	}
	for i := range svcList.Items {
		if err := r.Delete(ctx, &svcList.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}
	connList := &guacamolev1alpha1.GuacamoleConnectionList{}
	if err := r.List(ctx, connList, client.InNamespace(pool.Namespace), client.MatchingLabels{
		guacamolev1alpha1.DesktopLabelPool: pool.Name,
	}); err != nil {
		return ctrl.Result{}, err
	}
	for i := range connList.Items {
		if err := r.Delete(ctx, &connList.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}

	vms, err = r.listPoolVMs(ctx, pool)
	if err != nil {
		return ctrl.Result{}, err
	}
	if len(vms) > 0 {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	if err := r.cleanupCloneRBAC(ctx, pool); err != nil {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}

	controllerutil.RemoveFinalizer(pool, desktopPoolFinalizer)
	if err := r.Update(ctx, pool); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *DesktopPoolReconciler) listPoolVMs(ctx context.Context, pool *guacamolev1alpha1.DesktopPool) ([]unstructured.Unstructured, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "kubevirt.io",
		Version: "v1",
		Kind:    "VirtualMachineList",
	})
	if err := r.List(ctx, list, client.InNamespace(pool.Namespace), client.MatchingLabels{
		guacamolev1alpha1.DesktopLabelPool: pool.Name,
	}); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func (r *DesktopPoolReconciler) setVMStateLabel(ctx context.Context, vm *unstructured.Unstructured, state guacamolev1alpha1.DesktopState) error {
	labels := vm.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	if labels[guacamolev1alpha1.DesktopLabelState] == string(state) {
		return nil
	}
	labels[guacamolev1alpha1.DesktopLabelState] = string(state)
	vm.SetLabels(labels)
	return r.Update(ctx, vm)
}

func (r *DesktopPoolReconciler) patchStatus(ctx context.Context, pool *guacamolev1alpha1.DesktopPool, mutate func(*guacamolev1alpha1.DesktopPoolStatus)) error {
	latest := &guacamolev1alpha1.DesktopPool{}
	if err := r.Get(ctx, types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}, latest); err != nil {
		return err
	}
	mutate(&latest.Status)
	return r.Status().Update(ctx, latest)
}

func (r *DesktopPoolReconciler) prober() readiness.DesktopReadinessProber {
	if r.Prober != nil {
		return r.Prober
	}
	return readiness.TCPProber{Timeout: 3 * time.Second}
}

// SetupWithManager sets up the controller with the Manager.
func (r *DesktopPoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	vm := &unstructured.Unstructured{}
	vm.SetGroupVersionKind(virtualMachineGVK)
	return ctrl.NewControllerManagedBy(mgr).
		For(&guacamolev1alpha1.DesktopPool{}).
		Owns(vm).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&guacamolev1alpha1.GuacamoleConnection{}).
		Complete(r)
}

func validateDesktopPoolSpec(pool *guacamolev1alpha1.DesktopPool) error {
	if pool.Spec.Source.DataSource.Name == "" {
		return fmt.Errorf("spec.source.dataSource.name is required")
	}
	if pool.Spec.VirtualMachine.StorageClassName == "" {
		return fmt.Errorf("spec.virtualMachine.storageClassName is required")
	}
	if pool.Spec.Guacamole.InstanceRef.Name == "" {
		return fmt.Errorf("spec.guacamole.instanceRef.name is required")
	}
	if poolCreatesConnections(pool) {
		hasInline := pool.Spec.Guacamole.Password != ""
		hasRef := pool.Spec.Guacamole.PasswordSecretRef != nil && pool.Spec.Guacamole.PasswordSecretRef.Name != ""
		if !hasInline && !hasRef {
			return fmt.Errorf("spec.guacamole.password or spec.guacamole.passwordSecretRef is required when createConnections is enabled")
		}
	}
	if pool.Spec.VirtualMachine.DiskSize != "" {
		if _, err := resource.ParseQuantity(pool.Spec.VirtualMachine.DiskSize); err != nil {
			return fmt.Errorf("spec.virtualMachine.diskSize is invalid: %w", err)
		}
	}
	if pool.Spec.VirtualMachine.Memory != "" && pool.Spec.VirtualMachine.InstanceType == "" {
		if _, err := resource.ParseQuantity(pool.Spec.VirtualMachine.Memory); err != nil {
			return fmt.Errorf("spec.virtualMachine.memory is invalid: %w", err)
		}
	}
	return nil
}

func desktopPoolDesiredReplicas(pool *guacamolev1alpha1.DesktopPool) int32 {
	if pool.Spec.Replicas != nil {
		return *pool.Spec.Replicas
	}
	return 1
}

func poolCreatesConnections(pool *guacamolev1alpha1.DesktopPool) bool {
	if pool.Spec.CreateConnections == nil {
		return true
	}
	return *pool.Spec.CreateConnections
}

func desktopRDPPort(pool *guacamolev1alpha1.DesktopPool) int32 {
	if pool.Spec.Network.RDPPort != nil {
		return *pool.Spec.Network.RDPPort
	}
	return 3389
}

func rdpServiceName(vmName string) string {
	return fmt.Sprintf("%s-rdp", vmName)
}

func rdpServiceDNS(vmName, namespace string) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", rdpServiceName(vmName), namespace)
}

func desktopStateFromLabels(vm *unstructured.Unstructured) guacamolev1alpha1.DesktopState {
	labels := vm.GetLabels()
	if labels == nil {
		return guacamolev1alpha1.DesktopStateProvisioning
	}
	state := guacamolev1alpha1.DesktopState(labels[guacamolev1alpha1.DesktopLabelState])
	if state == "" {
		return guacamolev1alpha1.DesktopStateProvisioning
	}
	return state
}

func unstructuredConditionStatus(obj *unstructured.Unstructured, condType string) string {
	conditions, found, err := unstructured.NestedSlice(obj.Object, "status", "conditions")
	if !found || err != nil {
		return ""
	}
	for _, c := range conditions {
		m, ok := c.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := m["type"].(string); t == condType {
			status, _ := m["status"].(string)
			return status
		}
	}
	return ""
}

func endpointsHaveAddresses(eps *corev1.Endpoints) bool {
	for _, subset := range eps.Subsets {
		if len(subset.Addresses) > 0 {
			return true
		}
	}
	return false
}

type memberCounts struct {
	provisioning int32
	available    int32
	allocated    int32
	failed       int32
}

func summarizeMembers(members []guacamolev1alpha1.DesktopMemberStatus) memberCounts {
	var c memberCounts
	for _, m := range members {
		switch m.State {
		case guacamolev1alpha1.DesktopStateAvailable:
			c.available++
		case guacamolev1alpha1.DesktopStateAllocated, guacamolev1alpha1.DesktopStateInUse:
			c.allocated++
		case guacamolev1alpha1.DesktopStateFailed:
			c.failed++
		default:
			c.provisioning++
		}
	}
	return c
}

func setDesktopPoolCondition(status *guacamolev1alpha1.DesktopPoolStatus, ctype string, condStatus metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	for i := range status.Conditions {
		if status.Conditions[i].Type == ctype {
			if status.Conditions[i].Status == condStatus &&
				status.Conditions[i].Reason == reason &&
				status.Conditions[i].Message == message {
				return
			}
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

func randomSuffix(nBytes int) (string, error) {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func stringifyLabels(in map[string]interface{}) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = fmt.Sprint(v)
	}
	return out
}
