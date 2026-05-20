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
	"encoding/base64"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	autheliav1alpha1 "github.com/mnorrsken/heliop/api/v1alpha1"
)

// AutheliaOAuthClientReconciler reconciles a AutheliaOAuthClient object
type AutheliaOAuthClientReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=authelia.snosr.se,resources=autheliaoauthclients,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=authelia.snosr.se,resources=autheliaoauthclients/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=authelia.snosr.se,resources=autheliaoauthclients/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile ensures a Secret named "<name>-oauth-secret" exists in the client's
// namespace holding the generated client_id and client_secret. The secret value
// is generated once and preserved on subsequent reconciles.
func (r *AutheliaOAuthClientReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var oauthClient autheliav1alpha1.AutheliaOAuthClient
	if err := r.Get(ctx, req.NamespacedName, &oauthClient); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	clientID := oauthClient.Spec.ClientID
	if clientID == "" {
		clientID = oauthClient.Name
	}
	secretName := oauthClient.Name + "-oauth-secret"

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: oauthClient.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels["app.kubernetes.io/managed-by"] = "heliop"
		secret.Type = corev1.SecretTypeOpaque
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data["client_id"] = []byte(clientID)
		// Generate the client secret once for confidential clients; never rotate
		// it on subsequent reconciles. Public clients have no secret.
		if !oauthClient.Spec.Public && len(secret.Data["client_secret"]) == 0 {
			generated, genErr := generateSecret()
			if genErr != nil {
				return genErr
			}
			secret.Data["client_secret"] = []byte(generated)
		}
		if oauthClient.Spec.Public {
			delete(secret.Data, "client_secret")
		}
		return controllerutil.SetControllerReference(&oauthClient, secret, r.Scheme)
	})
	if err != nil {
		return ctrl.Result{}, err
	}

	if oauthClient.Status.SecretName != secretName {
		oauthClient.Status.SecretName = secretName
		if err := r.Status().Update(ctx, &oauthClient); err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// generateSecret returns a URL-safe random string suitable for an OIDC client
// secret.
func generateSecret() (string, error) {
	buf := make([]byte, 48)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AutheliaOAuthClientReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&autheliav1alpha1.AutheliaOAuthClient{}).
		Owns(&corev1.Secret{}).
		Named("autheliaoauthclient").
		Complete(r)
}
