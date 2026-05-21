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
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	autheliav1alpha1 "github.com/mnorrsken/heliop/api/v1alpha1"
)

// AutheliaReconciler reconciles a Authelia object
type AutheliaReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=authelia.snosr.se,resources=authelias,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=authelia.snosr.se,resources=authelias/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=authelia.snosr.se,resources=authelias/finalizers,verbs=update
// +kubebuilder:rbac:groups=authelia.snosr.se,resources=autheliaoauthclients,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services;configmaps;secrets,verbs=get;list;watch;create;update;patch;delete

// Reconcile renders the Authelia configuration (including OIDC clients sourced
// from AutheliaOAuthClient resources) and reconciles the Deployment, Service,
// ConfigMap and aggregated OIDC client Secret.
func (r *AutheliaReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var authelia autheliav1alpha1.Authelia
	if err := r.Get(ctx, req.NamespacedName, &authelia); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	clients, oidcData, missing, err := r.collectClients(ctx, &authelia)
	if err != nil {
		return ctrl.Result{}, err
	}

	renderedConfig, err := renderConfig(authelia.Spec.Config, clients, authelia.Spec.AuthenticationBackend)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.reconcileOIDCSecret(ctx, &authelia, oidcData); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileConfigMap(ctx, &authelia, renderedConfig); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileService(ctx, &authelia); err != nil {
		return ctrl.Result{}, err
	}
	readyReplicas, err := r.reconcileDeployment(ctx, &authelia)
	if err != nil {
		return ctrl.Result{}, err
	}

	clientIDs := make([]string, 0, len(clients))
	for _, c := range clients {
		clientIDs = append(clientIDs, c.spec.ClientID)
	}
	sort.Strings(clientIDs)
	if err := r.updateStatus(ctx, &authelia, readyReplicas, clientIDs); err != nil {
		return ctrl.Result{}, err
	}

	if missing > 0 {
		log.Info("waiting for OIDC client secrets to be generated", "pending", missing)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}
	return ctrl.Result{}, nil
}

// collectClients lists the OAuth clients targeting this Authelia, reads their
// generated secrets, and returns the rendered client list, the aggregated env
// data, and the number of clients still waiting for a secret.
func (r *AutheliaReconciler) collectClients(ctx context.Context, a *autheliav1alpha1.Authelia) ([]oidcClient, map[string][]byte, int, error) {
	var list autheliav1alpha1.AutheliaOAuthClientList
	if err := r.List(ctx, &list); err != nil {
		return nil, nil, 0, err
	}

	clients := make([]oidcClient, 0)
	data := map[string][]byte{}
	missing := 0

	for i := range list.Items {
		c := &list.Items[i]
		if !clientTargets(c, a) {
			continue
		}
		clientID := c.Spec.ClientID
		if clientID == "" {
			clientID = c.Name
		}
		spec := c.Spec
		spec.ClientID = clientID
		entry := oidcClient{spec: spec, envName: envVarName(clientID)}

		if c.Spec.Public {
			clients = append(clients, entry)
			continue
		}

		secret, err := r.clientSecretValue(ctx, c)
		if err != nil {
			return nil, nil, 0, err
		}
		if secret == "" {
			missing++
			continue
		}
		data[entry.envName] = []byte(secret)
		clients = append(clients, entry)
	}
	return clients, data, missing, nil
}

// clientSecretValue returns the plaintext client_secret stored in the client's
// generated Secret, or "" if it does not exist yet.
func (r *AutheliaReconciler) clientSecretValue(ctx context.Context, c *autheliav1alpha1.AutheliaOAuthClient) (string, error) {
	name := c.Status.SecretName
	if name == "" {
		name = c.Name + "-oauth-secret"
	}
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: c.Namespace, Name: name}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return string(secret.Data["client_secret"]), nil
}

func clientTargets(c *autheliav1alpha1.AutheliaOAuthClient, a *autheliav1alpha1.Authelia) bool {
	if c.Spec.AutheliaRef.Name != a.Name {
		return false
	}
	ns := c.Spec.AutheliaRef.Namespace
	if ns == "" {
		ns = c.Namespace
	}
	return ns == a.Namespace
}

func (r *AutheliaReconciler) reconcileOIDCSecret(ctx context.Context, a *autheliav1alpha1.Authelia, data map[string][]byte) error {
	desired := buildOIDCSecret(a, data)
	if err := controllerutil.SetControllerReference(a, desired, r.Scheme); err != nil {
		return err
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.Labels = desired.Labels
		secret.Type = desired.Type
		secret.Data = desired.Data
		return controllerutil.SetControllerReference(a, secret, r.Scheme)
	})
	return err
}

func (r *AutheliaReconciler) reconcileConfigMap(ctx context.Context, a *autheliav1alpha1.Authelia, rendered string) error {
	desired := buildConfigMap(a, rendered)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		cm.Labels = desired.Labels
		cm.Data = desired.Data
		return controllerutil.SetControllerReference(a, cm, r.Scheme)
	})
	return err
}

func (r *AutheliaReconciler) reconcileService(ctx context.Context, a *autheliav1alpha1.Authelia) error {
	desired := buildService(a)
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Labels = desired.Labels
		svc.Spec.Type = desired.Spec.Type
		svc.Spec.Selector = desired.Spec.Selector
		svc.Spec.Ports = desired.Spec.Ports
		return controllerutil.SetControllerReference(a, svc, r.Scheme)
	})
	return err
}

func (r *AutheliaReconciler) reconcileDeployment(ctx context.Context, a *autheliav1alpha1.Authelia) (int32, error) {
	desired := buildDeployment(a)
	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		dep.Labels = desired.Labels
		dep.Spec = desired.Spec
		return controllerutil.SetControllerReference(a, dep, r.Scheme)
	})
	if err != nil {
		return 0, err
	}
	return dep.Status.ReadyReplicas, nil
}

func (r *AutheliaReconciler) updateStatus(ctx context.Context, a *autheliav1alpha1.Authelia, ready int32, clientIDs []string) error {
	a.Status.ReadyReplicas = ready
	a.Status.OIDCClients = clientIDs
	return r.Status().Update(ctx, a)
}

// SetupWithManager sets up the controller with the Manager.
func (r *AutheliaReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&autheliav1alpha1.Authelia{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Watches(&autheliav1alpha1.AutheliaOAuthClient{}, handler.EnqueueRequestsFromMapFunc(mapClientToAuthelia)).
		Named("authelia").
		Complete(r)
}

// mapClientToAuthelia routes OAuth client events to the Authelia they target.
func mapClientToAuthelia(_ context.Context, obj client.Object) []reconcile.Request {
	c, ok := obj.(*autheliav1alpha1.AutheliaOAuthClient)
	if !ok {
		return nil
	}
	ns := c.Spec.AutheliaRef.Namespace
	if ns == "" {
		ns = c.Namespace
	}
	if c.Spec.AutheliaRef.Name == "" {
		return nil
	}
	return []reconcile.Request{{
		NamespacedName: types.NamespacedName{Namespace: ns, Name: c.Spec.AutheliaRef.Name},
	}}
}
