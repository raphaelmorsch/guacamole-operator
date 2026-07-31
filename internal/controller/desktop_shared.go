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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
)

func resolveDesktopCredentials(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	pool *guacamolev1alpha1.DesktopPool,
	owner client.Object,
) (*guacamolev1alpha1.SecretKeyRef, error) {
	guac := pool.Spec.Guacamole

	if guac.PasswordSecretRef != nil && guac.PasswordSecretRef.Name != "" {
		key := guac.PasswordSecretRef.Key
		if key == "" {
			key = "password"
		}
		secret := &corev1.Secret{}
		if err := c.Get(ctx, types.NamespacedName{Name: guac.PasswordSecretRef.Name, Namespace: pool.Namespace}, secret); err != nil {
			return nil, fmt.Errorf("passwordSecretRef %q: %w", guac.PasswordSecretRef.Name, err)
		}
		if _, ok := secret.Data[key]; !ok {
			return nil, fmt.Errorf("passwordSecretRef %q missing key %q", guac.PasswordSecretRef.Name, key)
		}
		return &guacamolev1alpha1.SecretKeyRef{Name: guac.PasswordSecretRef.Name, Key: key}, nil
	}

	if guac.Password == "" {
		return nil, fmt.Errorf("spec.guacamole.password or spec.guacamole.passwordSecretRef is required")
	}

	secretName := guac.CredentialsSecretName
	if secretName == "" {
		secretName = fmt.Sprintf("%s-rdp-credentials", pool.Name)
	}
	username := guac.Username
	if username == "" {
		username = "Administrator"
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: pool.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, c, secret, func() error {
		if owner != nil {
			if err := controllerutil.SetControllerReference(owner, secret, scheme); err != nil {
				return err
			}
		}
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[guacamolev1alpha1.DesktopLabelPool] = pool.Name
		secret.Labels[guacamolev1alpha1.DesktopLabelManagedBy] = guacamolev1alpha1.DesktopManagedByValue
		secret.Type = corev1.SecretTypeOpaque
		if secret.StringData == nil {
			secret.StringData = map[string]string{}
		}
		secret.StringData["username"] = username
		secret.StringData["password"] = guac.Password
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &guacamolev1alpha1.SecretKeyRef{Name: secretName, Key: "password"}, nil
}

func deleteDesktopResources(ctx context.Context, c client.Client, namespace, vmName string) error {
	// Delete any GuacamoleConnection named after the VM (provisional pool strategy).
	conn := &guacamolev1alpha1.GuacamoleConnection{}
	if err := c.Get(ctx, types.NamespacedName{Name: vmName, Namespace: namespace}, conn); err == nil {
		if err := c.Delete(ctx, conn); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	// Also delete connections labeled for this VM (session-owned).
	connList := &guacamolev1alpha1.GuacamoleConnectionList{}
	if err := c.List(ctx, connList, client.InNamespace(namespace), client.MatchingLabels{
		guacamolev1alpha1.DesktopLabelVM: vmName,
	}); err != nil {
		return err
	}
	for i := range connList.Items {
		if err := c.Delete(ctx, &connList.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}

	svc := &corev1.Service{}
	if err := c.Get(ctx, types.NamespacedName{Name: rdpServiceName(vmName), Namespace: namespace}, svc); err == nil {
		if err := c.Delete(ctx, svc); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}

	vm := &unstructured.Unstructured{}
	vm.SetGroupVersionKind(virtualMachineGVK)
	if err := c.Get(ctx, types.NamespacedName{Name: vmName, Namespace: namespace}, vm); err == nil {
		if err := c.Delete(ctx, vm); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

func sanitizeLabelValue(v string) string {
	if len(v) <= 63 {
		return v
	}
	return v[:63]
}
