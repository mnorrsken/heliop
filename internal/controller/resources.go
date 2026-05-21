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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	autheliav1alpha1 "github.com/mnorrsken/heliop/api/v1alpha1"
)

// initRenderScript renders /configuration.yaml through a shell here-doc so that
// $(envhash NAME) calls resolve OIDC client secrets to their Authelia password
// hashes at container start. Mirrors template/authelia/core/deployment.yaml.
const initRenderScript = `(
  cat <<'EOF'
function envhash() {
  authelia crypto hash generate --password "$(eval "echo \"\$$1\"")" | awk '{ print $2 }'
}
EOF
  EOFS=$(tr -dc A-Za-z0-9 </dev/urandom | head -c 13)
  echo "cat <<${EOFS}"
  cat /configuration.yaml
  echo "${EOFS}"
) | sh > /rendered-config/configuration.yaml`

func configMapName(a *autheliav1alpha1.Authelia) string { return a.Name + "-config" }

func oidcSecretName(a *autheliav1alpha1.Authelia) string { return a.Name + "-oidc-clients" }

// Mount points for first factor backend secrets. Each Secret is mounted as a
// directory so its keys appear as files; the configured key names the file.
const (
	fileBackendMountPath  = "/authelia/file-backend"
	ldapPasswordMountPath = "/authelia/ldap"
	ldapPasswordEnvVar    = "AUTHELIA_AUTHENTICATION_BACKEND_LDAP_PASSWORD_FILE"
)

func fileUsersPath(ref autheliav1alpha1.SecretKeyRef) string {
	return fileBackendMountPath + "/" + ref.Key
}

func ldapPasswordPath(ref autheliav1alpha1.SecretKeyRef) string {
	return ldapPasswordMountPath + "/" + ref.Key
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
// Deployment's init container.
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

// buildOIDCSecret aggregates the plaintext OIDC client secrets into a single
// Secret in the Authelia namespace, exposed to the init container via envFrom.
func buildOIDCSecret(a *autheliav1alpha1.Authelia, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      oidcSecretName(a),
			Namespace: a.Namespace,
			Labels:    labelsFor(a),
		},
		Type: corev1.SecretTypeOpaque,
		Data: data,
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

func buildDeployment(a *autheliav1alpha1.Authelia) *appsv1.Deployment {
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
	secretName := d.SecretName
	if secretName == "" {
		secretName = "authelia"
	}
	pgSecret := d.PostgresSecretName
	if pgSecret == "" {
		pgSecret = "authelia-db-app"
	}
	redisSecret := d.RedisSecretName
	if redisSecret == "" {
		redisSecret = "redis-ha"
	}
	backend := a.Spec.AuthenticationBackend

	podLabels := selectorFor(a)
	podLabels["redis-ha-client"] = "true"

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

	mainMounts := []corev1.VolumeMount{
		{Name: "config", MountPath: "/configuration.yaml", SubPath: "configuration.yaml", ReadOnly: true},
		{Name: "authelia-secret", MountPath: "/secrets", ReadOnly: true},
		{Name: "redis-secret", MountPath: "/redis-secret", ReadOnly: true},
		{Name: "pg-secret", MountPath: "/pg-secret", ReadOnly: true},
	}
	volumes := []corev1.Volume{
		{Name: "authelia-secret", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: secretName}}},
		{Name: "config", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "config-template", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: configMapName(a)}}}},
		{Name: "pg-secret", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: pgSecret}}},
		{Name: "redis-secret", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: redisSecret}}},
	}

	// Mount the first factor backend Secret(s) so the users file / LDAP bind
	// password are available to the Authelia container.
	if backend != nil {
		switch {
		case backend.File != nil:
			volumes = append(volumes, corev1.Volume{Name: "file-backend", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: backend.File.UsersSecret.Name}}})
			mainMounts = append(mainMounts, corev1.VolumeMount{Name: "file-backend", MountPath: fileBackendMountPath, ReadOnly: true})
		case backend.LDAP != nil:
			volumes = append(volumes, corev1.Volume{Name: "ldap-password", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: backend.LDAP.PasswordSecret.Name}}})
			mainMounts = append(mainMounts, corev1.VolumeMount{Name: "ldap-password", MountPath: ldapPasswordMountPath, ReadOnly: true})
		}
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
				ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
				Spec: corev1.PodSpec{
					EnableServiceLinks: ptr(false),
					InitContainers: []corev1.Container{{
						Name:            "init",
						Image:           image,
						ImagePullPolicy: pullPolicy,
						Command:         []string{"sh", "-xec", initRenderScript},
						EnvFrom: []corev1.EnvFromSource{
							{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: secretName}}},
							{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: oidcSecretName(a)}}},
						},
						VolumeMounts: []corev1.VolumeMount{
							{Name: "config", MountPath: "/rendered-config"},
							{Name: "config-template", MountPath: "/configuration.yaml", SubPath: "configuration.yaml", ReadOnly: true},
						},
					}},
					Containers: []corev1.Container{{
						Name:            "authelia",
						Image:           image,
						ImagePullPolicy: pullPolicy,
						Command:         []string{"authelia"},
						Args:            []string{"--config=/configuration.yaml"},
						Env:             autheliaFileEnv(backend),
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

// autheliaFileEnv lists the secret-file env references used by Authelia, mirroring
// the upstream deployment template. The LDAP password reference depends on the
// configured authentication backend: it points at the mounted backend Secret
// when LDAP is configured first-class, is omitted entirely for the file backend,
// and falls back to the legacy /secrets/LDAP_PASSWORD path otherwise.
func autheliaFileEnv(backend *autheliav1alpha1.AuthenticationBackendSpec) []corev1.EnvVar {
	env := []corev1.EnvVar{
		{Name: "AUTHELIA_SERVER_DISABLE_HEALTHCHECK", Value: "true"},
		{Name: "AUTHELIA_SESSION_SECRET_FILE", Value: "/secrets/SESSION_ENCRYPTION_KEY"},
		{Name: "AUTHELIA_NOTIFIER_SMTP_PASSWORD_FILE", Value: "/secrets/SMTP_PASSWORD"},
		{Name: "AUTHELIA_STORAGE_ENCRYPTION_KEY_FILE", Value: "/secrets/STORAGE_ENCRYPTION_KEY"},
		{Name: "AUTHELIA_IDENTITY_PROVIDERS_OIDC_HMAC_SECRET_FILE", Value: "/secrets/OIDC_HMAC_SECRET"},
		{Name: "AUTHELIA_IDENTITY_PROVIDERS_OIDC_ISSUER_PRIVATE_KEY_FILE", Value: "/secrets/OIDC_PRIVATE_KEY"},
		{Name: "AUTHELIA_SESSION_REDIS_PASSWORD_FILE", Value: "/redis-secret/redis-password"},
		{Name: "AUTHELIA_STORAGE_POSTGRES_PASSWORD_FILE", Value: "/pg-secret/password"},
	}

	switch {
	case backend == nil:
		env = append(env, corev1.EnvVar{Name: ldapPasswordEnvVar, Value: "/secrets/LDAP_PASSWORD"})
	case backend.LDAP != nil:
		env = append(env, corev1.EnvVar{Name: ldapPasswordEnvVar, Value: ldapPasswordPath(backend.LDAP.PasswordSecret)})
	}
	return env
}

func ptr[T any](v T) *T { return &v }
