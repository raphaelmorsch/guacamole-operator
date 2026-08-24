package controller

import (
	"context"
	"fmt"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	guacamolev1alpha1 "github.com/raphaelmorsch/guacamole-operator/api/v1alpha1"
	"github.com/raphaelmorsch/guacamole-operator/internal/branding"
)

const (
	brandingJarVolume = "login-branding-jar"
	guacamoleHomePath = "/guacamole-home"
)

func brandingJarSecretName(name string) string {
	return name + "-login-branding-jar"
}

// Legacy config map from releases before the operator built the JAR directly.
func brandingConfigMapName(name string) string {
	return name + "-login-branding"
}

func (r *GuacamoleReconciler) reconcileLoginBranding(ctx context.Context, guac *guacamolev1alpha1.Guacamole) error {
	if !loginBrandingConfigured(guac) {
		if err := r.deleteBrandingSecretIfExists(ctx, guac); err != nil {
			return err
		}
		return r.deleteConfigMapIfExists(ctx, guac, brandingConfigMapName(guac.Name))
	}

	opts, err := brandingOptions(guac)
	if err != nil {
		return err
	}

	logoPNG, err := r.brandingLogoBytes(ctx, guac)
	if err != nil {
		return fmt.Errorf("read branding logo: %w", err)
	}

	jar, err := branding.BuildJAR(opts, logoPNG)
	if err != nil {
		return fmt.Errorf("build branding jar: %w", err)
	}

	secretName := brandingJarSecretName(guac.Name)
	secret := &corev1.Secret{}
	secret.Name = secretName
	secret.Namespace = guac.Namespace
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if err := controllerutil.SetControllerReference(guac, secret, r.Scheme); err != nil {
			return err
		}
		if secret.CreationTimestamp.IsZero() {
			secret.Type = corev1.SecretTypeOpaque
		}
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Labels = labelsFor(guac, "guacamole")
		secret.Data[branding.JARFileName] = jar
		return nil
	})
	if err != nil {
		return err
	}

	return r.deleteConfigMapIfExists(ctx, guac, brandingConfigMapName(guac.Name))
}

func (r *GuacamoleReconciler) deleteBrandingSecretIfExists(ctx context.Context, guac *guacamolev1alpha1.Guacamole) error {
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      brandingJarSecretName(guac.Name),
		Namespace: guac.Namespace,
	}, secret)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.Delete(ctx, secret)
}

func (r *GuacamoleReconciler) brandingLogoBytes(ctx context.Context, guac *guacamolev1alpha1.Guacamole) ([]byte, error) {
	b := guac.Spec.LoginBranding
	if !loginBrandingHasLogo(b) {
		return nil, nil
	}

	ns := types.NamespacedName{Namespace: guac.Namespace}
	switch b.LogoSource {
	case "secret":
		if b.LogoSecretRef == nil || b.LogoSecretRef.Name == "" {
			return nil, fmt.Errorf("logoSecretRef.name is required when logoSource is secret")
		}
		key := logoRefKey(b.LogoSecretRef)
		secret := &corev1.Secret{}
		ns.Name = b.LogoSecretRef.Name
		if err := r.Get(ctx, ns, secret); err != nil {
			return nil, err
		}
		data, ok := secret.Data[key]
		if !ok {
			return nil, fmt.Errorf("secret %s does not contain key %q", ns.Name, key)
		}
		return branding.DecodeLogoBytes(data)
	case "configMap":
		if b.LogoConfigMapRef == nil || b.LogoConfigMapRef.Name == "" {
			return nil, fmt.Errorf("logoConfigMapRef.name is required when logoSource is configMap")
		}
		key := logoRefKey(b.LogoConfigMapRef)
		cm := &corev1.ConfigMap{}
		ns.Name = b.LogoConfigMapRef.Name
		if err := r.Get(ctx, ns, cm); err != nil {
			return nil, err
		}
		data, ok := cm.BinaryData[key]
		if !ok {
			if text, textOK := cm.Data[key]; textOK {
				data = []byte(text)
				ok = true
			}
		}
		if !ok {
			return nil, fmt.Errorf("configmap %s does not contain key %q", ns.Name, key)
		}
		return branding.DecodeLogoBytes(data)
	default:
		return nil, nil
	}
}

func (r *GuacamoleReconciler) deleteConfigMapIfExists(ctx context.Context, guac *guacamolev1alpha1.Guacamole, name string) error {
	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: guac.Namespace}, cm)
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return r.Delete(ctx, cm)
}

func loginBrandingEnabled(b *guacamolev1alpha1.LoginBrandingSpec) bool {
	if b == nil || b.Enabled == nil {
		return false
	}
	return *b.Enabled
}

func loginBrandingConfigured(g *guacamolev1alpha1.Guacamole) bool {
	b := g.Spec.LoginBranding
	if !loginBrandingEnabled(b) {
		return false
	}
	if strings.TrimSpace(b.Title) != "" {
		return true
	}
	return loginBrandingHasLogo(b)
}

func loginBrandingHasLogo(b *guacamolev1alpha1.LoginBrandingSpec) bool {
	if b == nil || !loginBrandingEnabled(b) {
		return false
	}
	switch b.LogoSource {
	case "secret":
		return b.LogoSecretRef != nil && b.LogoSecretRef.Name != ""
	case "configMap":
		return b.LogoConfigMapRef != nil && b.LogoConfigMapRef.Name != ""
	default:
		return false
	}
}

func logoRefKey(ref *guacamolev1alpha1.SecretKeyRef) string {
	if ref == nil {
		return "logo"
	}
	if ref.Key != "" {
		return ref.Key
	}
	return "logo"
}

func brandingOptions(g *guacamolev1alpha1.Guacamole) (branding.Options, error) {
	b := g.Spec.LoginBranding
	opts := branding.Options{
		Title:   strings.TrimSpace(b.Title),
		HasLogo: loginBrandingHasLogo(b),
	}
	return opts, nil
}

func applyLoginBranding(g *guacamolev1alpha1.Guacamole, deploy *appsv1.Deployment) {
	if !loginBrandingConfigured(g) {
		return
	}

	pod := &deploy.Spec.Template.Spec
	mode := int32(0444)
	pod.Volumes = append(pod.Volumes,
		corev1.Volume{
			Name: brandingJarVolume,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  brandingJarSecretName(g.Name),
					DefaultMode: &mode,
				},
			},
		},
	)

	for i := range pod.Containers {
		if pod.Containers[i].Name != "guacamole" {
			continue
		}
		for j := range pod.Containers[i].Env {
			if pod.Containers[i].Env[j].Name == "GUACAMOLE_HOME" {
				pod.Containers[i].Env[j].Value = guacamoleHomePath
				break
			}
		}
		pod.Containers[i].VolumeMounts = append(pod.Containers[i].VolumeMounts,
			corev1.VolumeMount{
				Name:      brandingJarVolume,
				MountPath: guacamoleHomePath + "/extensions",
				ReadOnly:  true,
			},
		)
	}
}
