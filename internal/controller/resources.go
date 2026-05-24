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
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	autheliav1alpha1 "github.com/mnorrsken/heliop/api/v1alpha1"
)

func configMapName(a *autheliav1alpha1.Authelia) string { return a.Name + "-config" }

func dataPVCName(a *autheliav1alpha1.Authelia) string { return a.Name + "-data" }

// buildDataPVC builds the operator-managed PersistentVolumeClaim mounted at
// /data from the configured volumeClaimTemplate.
func buildDataPVC(a *autheliav1alpha1.Authelia) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dataPVCName(a),
			Namespace: a.Namespace,
			Labels:    labelsFor(a),
		},
		Spec: *a.Spec.Deployment.VolumeClaimTemplate,
	}
}

// Secrets are mounted as directories so their keys appear as files; the
// configured key names the file.
const (
	fileBackendMountPath = "/authelia/file-backend"
	secretsMountBase     = "/authelia/secrets"
)

func fileUsersPath(ref autheliav1alpha1.SecretKeyRef) string {
	return fileBackendMountPath + "/" + ref.Key
}

// isFileSecret reports whether the env variable expects a file path (the
// Authelia *_FILE convention), in which case the Secret is mounted.
func isFileSecret(envName string) bool { return strings.HasSuffix(envName, "_FILE") }

// secretVolumeName derives a DNS-1123 volume name from an env variable name.
func secretVolumeName(envName string) string {
	b := make([]rune, 0, len(envName))
	for _, r := range strings.ToLower(envName) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b = append(b, r)
		} else {
			b = append(b, '-')
		}
	}
	name := strings.Trim(string(b), "-")
	if len(name) > 63 {
		name = strings.Trim(name[:63], "-")
	}
	return name
}

func secretMountPath(envName string) string {
	return secretsMountBase + "/" + secretVolumeName(envName)
}

func labelsFor(a *autheliav1alpha1.Authelia) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "authelia",
		"app.kubernetes.io/instance":   a.Name,
		"app.kubernetes.io/managed-by": "heliop",
	}
}

func selectorFor(a *autheliav1alpha1.Authelia) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":     "authelia",
		"app.kubernetes.io/instance": a.Name,
	}
}

func defaultResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("50Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("200Mi"),
		},
	}
}

// buildConfigMap holds the rendered Authelia configuration consumed by the
// Authelia container.
func buildConfigMap(a *autheliav1alpha1.Authelia, renderedConfig string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName(a),
			Namespace: a.Namespace,
			Labels:    labelsFor(a),
		},
		Data: map[string]string{
			"configuration.yaml": renderedConfig,
		},
	}
}

func buildService(a *autheliav1alpha1.Authelia) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      a.Name,
			Namespace: a.Namespace,
			Labels:    labelsFor(a),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selectorFor(a),
			Ports: []corev1.ServicePort{{
				Name:       "http",
				Protocol:   corev1.ProtocolTCP,
				Port:       80,
				TargetPort: intstr.FromString("http"),
			}},
		},
	}
}

func buildDeployment(a *autheliav1alpha1.Authelia, oidcEnabled bool, configChecksum string) *appsv1.Deployment {
	d := a.Spec.Deployment
	replicas := int32(2)
	if d.Replicas != nil {
		replicas = *d.Replicas
	}
	image := d.Image
	if image == "" {
		image = "ghcr.io/authelia/authelia:4"
	}
	pullPolicy := d.ImagePullPolicy
	if pullPolicy == "" {
		pullPolicy = corev1.PullIfNotPresent
	}
	resources := d.Resources
	if resources.Requests == nil && resources.Limits == nil {
		resources = defaultResources()
	}
	secretName := coreSecretName(a)
	settings := a.Spec.Settings

	podLabels := selectorFor(a)

	probe := func(initialDelay, period int32) *corev1.Probe {
		return &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/api/health",
					Port:   intstr.FromString("http"),
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: initialDelay,
			PeriodSeconds:       period,
			TimeoutSeconds:      5,
			SuccessThreshold:    1,
			FailureThreshold:    5,
		}
	}
	startup := probe(10, 5)
	startup.FailureThreshold = 6

	// /data holds persistent state (e.g. the SQLite database). Use the
	// operator-managed PVC when a volumeClaimTemplate is set, else an emptyDir.
	dataVolume := corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}
	if d.VolumeClaimTemplate != nil {
		dataVolume = corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: dataPVCName(a)}}
	}

	mainMounts := []corev1.VolumeMount{
		{Name: "config", MountPath: "/configuration.yaml", SubPath: "configuration.yaml", ReadOnly: true},
		{Name: "authelia-secret", MountPath: "/secrets", ReadOnly: true},
		{Name: "data", MountPath: "/data"},
	}
	volumes := []corev1.Volume{
		{Name: "authelia-secret", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: secretName}}},
		{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: configMapName(a)}}}},
		{Name: "data", VolumeSource: dataVolume},
	}

	// Mount the file backend users database when configured.
	if settings.FileUsersSecret != nil {
		volumes = append(volumes, corev1.Volume{Name: "file-backend", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: settings.FileUsersSecret.Name}}})
		mainMounts = append(mainMounts, corev1.VolumeMount{Name: "file-backend", MountPath: fileBackendMountPath, ReadOnly: true})
	}

	// Mount each _FILE secret so Authelia can read it from the mounted path.
	for _, s := range settings.Secrets {
		if s.Secret == nil || !isFileSecret(s.Name) {
			continue
		}
		name := secretVolumeName(s.Name)
		volumes = append(volumes, corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: s.Secret.Name}}})
		mainMounts = append(mainMounts, corev1.VolumeMount{Name: name, MountPath: secretMountPath(s.Name), ReadOnly: true})
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      a.Name,
			Namespace: a.Namespace,
			Labels:    labelsFor(a),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas:             &replicas,
			RevisionHistoryLimit: ptr(int32(1)),
			Selector:             &metav1.LabelSelector{MatchLabels: selectorFor(a)},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels:      podLabels,
					Annotations: map[string]string{"heliop.snosr.se/config-checksum": configChecksum},
				},
				Spec: corev1.PodSpec{
					EnableServiceLinks: ptr(false),
					Containers: []corev1.Container{{
						Name:            "authelia",
						Image:           image,
						ImagePullPolicy: pullPolicy,
						Command:         []string{"authelia"},
						Args:            []string{"--config=/configuration.yaml"},
						Env:             autheliaFileEnv(settings, oidcEnabled),
						Resources:       resources,
						Ports: []corev1.ContainerPort{{
							Name:          "http",
							ContainerPort: 9091,
							Protocol:      corev1.ProtocolTCP,
						}},
						StartupProbe:   startup,
						LivenessProbe:  probe(0, 30),
						ReadinessProbe: probe(0, 5),
						VolumeMounts:   mainMounts,
					}},
					Volumes: volumes,
				},
			},
		},
	}
}

// autheliaFileEnv lists the env references used by Authelia. The core secrets
// (session, storage encryption key) are always referenced; the OIDC secrets
// only when clients are configured (they implicitly enable the identity
// provider, which then requires a client). Each settings.secrets entry adds its
// variable: a file path for _FILE variables (the Secret is mounted), or the
// Secret value directly otherwise.
func autheliaFileEnv(settings autheliav1alpha1.AutheliaSettings, oidcEnabled bool) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "AUTHELIA_SERVER_DISABLE_HEALTHCHECK", Value: "true"},
		{Name: "AUTHELIA_SESSION_SECRET_FILE", Value: "/secrets/SESSION_ENCRYPTION_KEY"},
		{Name: "AUTHELIA_STORAGE_ENCRYPTION_KEY_FILE", Value: "/secrets/STORAGE_ENCRYPTION_KEY"},
	}

	if oidcEnabled {
		env = append(env,
			corev1.EnvVar{Name: "AUTHELIA_IDENTITY_PROVIDERS_OIDC_HMAC_SECRET_FILE", Value: "/secrets/OIDC_HMAC_SECRET"},
			corev1.EnvVar{Name: "AUTHELIA_IDENTITY_PROVIDERS_OIDC_ISSUER_PRIVATE_KEY_FILE", Value: "/secrets/OIDC_PRIVATE_KEY"},
		)
	}

	for _, s := range settings.Secrets {
		if s.Secret == nil {
			continue
		}
		if isFileSecret(s.Name) {
			env = append(env, corev1.EnvVar{Name: s.Name, Value: secretMountPath(s.Name) + "/" + s.Secret.Key})
		} else {
			env = append(env, corev1.EnvVar{Name: s.Name, ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: s.Secret.Name},
					Key:                  s.Secret.Key,
				},
			}})
		}
	}
	return env
}

func ptr[T any](v T) *T { return &v }
