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
	"strings"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

const (
	// desktopPoolCloneServiceAccount is used by VirtualMachines for CDI cross-namespace clones.
	desktopPoolCloneServiceAccount = "guacamole-desktop-pool"

	// Shared Role in the golden-image (DataSource) namespace.
	desktopPoolCloneRoleName = "guacamole-desktop-cloner"
)

// dataSourceNamespace resolves the golden-image namespace for clone RBAC.
func dataSourceNamespace(pool *guacamolev1alpha1.DesktopPool) string {
	if pool.Spec.Source.DataSource.Namespace != "" {
		return pool.Spec.Source.DataSource.Namespace
	}
	return pool.Namespace
}

func desktopPoolCloneRoleBindingName(poolNamespace string) string {
	return fmt.Sprintf("guacamole-desktop-cloner-%s", poolNamespace)
}

// ensureCloneRBAC provisions CDI cross-namespace clone permissions in the
// DataSource (golden image) namespace for this pool's ServiceAccount.
//
// CDI requires the destination namespace SA to get PVCs (and create pods) in the
// source namespace before a DataVolume can clone from a DataSource there.
func (r *DesktopPoolReconciler) ensureCloneRBAC(ctx context.Context, pool *guacamolev1alpha1.DesktopPool) error {
	if err := r.ensureCloneServiceAccount(ctx, pool); err != nil {
		return err
	}

	srcNS := dataSourceNamespace(pool)
	if srcNS == pool.Namespace {
		// Same-namespace clones do not need cross-namespace RoleBindings.
		return nil
	}

	if err := r.ensureCloneRole(ctx, srcNS); err != nil {
		return err
	}
	return r.ensureCloneRoleBinding(ctx, pool, srcNS)
}

func (r *DesktopPoolReconciler) ensureCloneServiceAccount(ctx context.Context, pool *guacamolev1alpha1.DesktopPool) error {
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desktopPoolCloneServiceAccount,
			Namespace: pool.Namespace,
		},
	}
	// Shared per namespace (not owned by a single pool) so multiple DesktopPools can reuse it.
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sa, func() error {
		if sa.Labels == nil {
			sa.Labels = map[string]string{}
		}
		sa.Labels[guacamolev1alpha1.DesktopLabelManagedBy] = guacamolev1alpha1.DesktopManagedByValue
		return nil
	})
	return err
}

func (r *DesktopPoolReconciler) ensureCloneRole(ctx context.Context, sourceNamespace string) error {
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desktopPoolCloneRoleName,
			Namespace: sourceNamespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		if role.Labels == nil {
			role.Labels = map[string]string{}
		}
		role.Labels[guacamolev1alpha1.DesktopLabelManagedBy] = guacamolev1alpha1.DesktopManagedByValue
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"persistentvolumeclaims"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{""},
				Resources: []string{"pods"},
				Verbs:     []string{"create"},
			},
			{
				APIGroups: []string{"cdi.kubevirt.io"},
				Resources: []string{"datavolumes", "datasources"},
				Verbs:     []string{"get", "list", "watch"},
			},
		}
		return nil
	})
	return err
}

func (r *DesktopPoolReconciler) ensureCloneRoleBinding(ctx context.Context, pool *guacamolev1alpha1.DesktopPool, sourceNamespace string) error {
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desktopPoolCloneRoleBindingName(pool.Namespace),
			Namespace: sourceNamespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		if rb.Labels == nil {
			rb.Labels = map[string]string{}
		}
		rb.Labels[guacamolev1alpha1.DesktopLabelManagedBy] = guacamolev1alpha1.DesktopManagedByValue
		rb.Labels["desktop.guacamole.io/pool-namespace"] = pool.Namespace

		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     desktopPoolCloneRoleName,
		}

		// Dedicated SA used by new VMs, plus default for VMs created before the SA existed.
		desired := []rbacv1.Subject{
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      desktopPoolCloneServiceAccount,
				Namespace: pool.Namespace,
			},
			{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      "default",
				Namespace: pool.Namespace,
			},
		}
		rb.Subjects = mergeSubjects(rb.Subjects, desired)
		return nil
	})
	return err
}

// cleanupCloneRBAC removes the RoleBinding in the golden-image namespace when no
// other DesktopPool in the same namespace still needs it. The shared Role is kept
// if any RoleBinding remains.
func (r *DesktopPoolReconciler) cleanupCloneRBAC(ctx context.Context, pool *guacamolev1alpha1.DesktopPool) error {
	srcNS := dataSourceNamespace(pool)
	if srcNS == "" || srcNS == pool.Namespace {
		return nil
	}

	stillNeeded, err := r.cloneRBACStillNeeded(ctx, pool, srcNS)
	if err != nil {
		return err
	}
	if stillNeeded {
		return nil
	}

	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desktopPoolCloneRoleBindingName(pool.Namespace),
			Namespace: srcNS,
		},
	}
	if err := r.Delete(ctx, rb); err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	// Drop the shared Role only when no cloner RoleBindings remain in the source ns.
	rbList := &rbacv1.RoleBindingList{}
	if err := r.List(ctx, rbList, client.InNamespace(srcNS), client.MatchingLabels{
		guacamolev1alpha1.DesktopLabelManagedBy: guacamolev1alpha1.DesktopManagedByValue,
	}); err != nil {
		return err
	}
	for i := range rbList.Items {
		if strings.HasPrefix(rbList.Items[i].Name, "guacamole-desktop-cloner-") {
			return nil
		}
	}

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desktopPoolCloneRoleName,
			Namespace: srcNS,
		},
	}
	if err := r.Delete(ctx, role); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (r *DesktopPoolReconciler) cloneRBACStillNeeded(ctx context.Context, pool *guacamolev1alpha1.DesktopPool, sourceNamespace string) (bool, error) {
	list := &guacamolev1alpha1.DesktopPoolList{}
	if err := r.List(ctx, list, client.InNamespace(pool.Namespace)); err != nil {
		return false, err
	}
	for i := range list.Items {
		other := &list.Items[i]
		if other.Name == pool.Name {
			continue
		}
		if !other.DeletionTimestamp.IsZero() {
			continue
		}
		if dataSourceNamespace(other) == sourceNamespace {
			return true, nil
		}
	}
	return false, nil
}

func mergeSubjects(existing, desired []rbacv1.Subject) []rbacv1.Subject {
	out := make([]rbacv1.Subject, 0, len(existing)+len(desired))
	seen := map[string]struct{}{}
	add := func(s rbacv1.Subject) {
		key := s.Kind + "/" + s.Namespace + "/" + s.Name
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	for _, s := range existing {
		add(s)
	}
	for _, s := range desired {
		add(s)
	}
	return out
}
