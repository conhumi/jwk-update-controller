/*


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

package controllers

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/lestrrat-go/jwx/jwk"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	jucv1alpha1 "github.com/conhumi/jwk-update-controller/api/v1alpha1"
)

const parseTimeLayout = time.RFC3339

// JWKReconciler reconciles a JWK object
type JWKReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func (r *JWKReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&jucv1alpha1.JWK{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}

// +kubebuilder:rbac:groups=juc.conhumi.net,resources=jwks,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=juc.conhumi.net,resources=jwks/status,verbs=get;update;patch

func (r *JWKReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	r.Log = r.Log.WithValues("jwk", req.NamespacedName)

	object := jucv1alpha1.JWK{}
	if err := r.Client.Get(ctx, req.NamespacedName, &object); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		r.Log.Error(err, "get jwk resource failed", "namespacedName", req.NamespacedName)
		return ctrl.Result{}, err
	}

	desire := object.DeepCopy()

	validJWKs, err := r.getValidJWKs(ctx, object.Status)
	if err != nil {
		r.Log.Error(err, "get valid jwks failed")
		return ctrl.Result{}, err
	}

	expiredJWKs, err := r.getExpiredJWKs(ctx, object.Status)
	if err != nil {
		r.Log.Error(err, "get expired jwks failed")
		return ctrl.Result{}, err
	}

	renewalNeededJWKs, err := r.getRenewalNeededJWKs(ctx, object)
	if err != nil {
		r.Log.Error(err, "get renewal needed jwks failed")
		return ctrl.Result{}, err
	}

	if len(expiredJWKs) > 1 {
		for _, expiredJWK := range expiredJWKs {
			if err := r.deleteJWKSecret(ctx, expiredJWK); err != nil &&
				!k8serrors.IsNotFound(err) {
				r.Log.Error(err, "delete expired jwk failed")
				return ctrl.Result{}, err
			}
		}
		desire.Status.JWKs = validJWKs
		return ctrl.Result{}, r.Client.Update(ctx, desire)
	}

	if len(validJWKs) < 1 || len(validJWKs) == len(renewalNeededJWKs) {
		r.Log.Info("create new jwk")
		newJWK, err := r.createJWKSecret(ctx, desire.ObjectMeta, desire.Spec)
		if err != nil {
			r.Log.Error(err, "create jwk failed", "spec", desire.Spec)
			return ctrl.Result{}, err
		}
		desire.Status.JWKs = append(desire.Status.JWKs, newJWK)
		return ctrl.Result{}, r.Client.Update(ctx, desire)
	}

	return ctrl.Result{Requeue: true, RequeueAfter: 30 * time.Second}, nil
}

func (r *JWKReconciler) getValidJWKs(ctx context.Context, jwkStatus jucv1alpha1.JWKStatus) ([]jucv1alpha1.Secret, error) {
	validJWKs := []jucv1alpha1.Secret{}
	for _, jwkSecret := range jwkStatus.JWKs {
		if jwkSecret.ExpireAt != "" {
			expireAt, err := time.Parse(parseTimeLayout, jwkSecret.ExpireAt)
			if err != nil {
				r.Log.Error(err, "parse expiredAt failed", "value", jwkSecret.ExpireAt)
				return []jucv1alpha1.Secret{}, err
			}
			if time.Now().After(expireAt) {
				continue
			}
		}
		validJWKs = append(validJWKs, jwkSecret)
	}
	return validJWKs, nil
}

func (r *JWKReconciler) getExpiredJWKs(ctx context.Context, jwkStatus jucv1alpha1.JWKStatus) ([]jucv1alpha1.Secret, error) {
	expiredJWKs := []jucv1alpha1.Secret{}
	for _, jwkSecret := range jwkStatus.JWKs {
		expireAt, err := time.Parse(parseTimeLayout, jwkSecret.ExpireAt)
		if err != nil {
			r.Log.Error(err, "parse expiredAt failed", "value", jwkSecret.ExpireAt)
			return []jucv1alpha1.Secret{}, err
		}
		if time.Now().After(expireAt) {
			expiredJWKs = append(expiredJWKs, jwkSecret)
		}
	}
	return expiredJWKs, nil
}

func (r *JWKReconciler) getRenewalNeededJWKs(ctx context.Context, jwk jucv1alpha1.JWK) ([]jucv1alpha1.Secret, error) {
	renewalNeededJWKs := []jucv1alpha1.Secret{}
	for _, jwkSecret := range jwk.Status.JWKs {
		expireAt, err := time.Parse(parseTimeLayout, jwkSecret.ExpireAt)
		if err != nil {
			r.Log.Error(err, "parse expiredAt failed", "value", jwkSecret.ExpireAt)
			return []jucv1alpha1.Secret{}, err
		}
		rotateAt := expireAt.Add(time.Duration(-1*jwk.Spec.RotateDurationHoursBeforeExpired) * time.Hour)
		if time.Now().After(rotateAt) {
			renewalNeededJWKs = append(renewalNeededJWKs)
		}
	}
	return renewalNeededJWKs, nil
}

func (r *JWKReconciler) createJWKSecret(
	ctx context.Context,
	objMeta metav1.ObjectMeta,
	jwkSpec jucv1alpha1.JWKSpec) (jucv1alpha1.Secret, error) {
	createdAt := time.Now()

	privateKey, err := generatePrivateKey(jwkSpec)
	if err != nil {
		r.Log.Error(err, "create private key failed")
		return jucv1alpha1.Secret{}, err
	}

	privateKeyJWK, err := jwk.New(privateKey)
	if err != nil {
		r.Log.Error(err, "convert jwk failed")
		return jucv1alpha1.Secret{}, err
	}

	if jwkSpec.PublicKeyUse != "" {
		if err := privateKeyJWK.Set(jwk.KeyUsageKey, jwkSpec.PublicKeyUse); err != nil {
			r.Log.Error(err, "set public key use failed")
			return jucv1alpha1.Secret{}, err
		}
	}
	if jwkSpec.KeyOperations != "" {
		if err := privateKeyJWK.Set(jwk.KeyOpsKey, jwkSpec.KeyOperations); err != nil {
			r.Log.Error(err, "set public key use failed")
			return jucv1alpha1.Secret{}, err
		}
	}

	privateKeyJWKJson, err := json.Marshal(privateKeyJWK)
	if err != nil {
		r.Log.Error(err, "convert json failed")
		return jucv1alpha1.Secret{}, err
	}

	secretName := fmt.Sprintf("%s-%s", objMeta.Name, createdAt.Format("2006-01-02-15-04-05"))
	newSecret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: objMeta.Namespace,
			Name:      secretName,
		},
		StringData: map[string]string{
			"privateKeyJWK": string(privateKeyJWKJson),
		},
	}
	if err := r.Client.Create(ctx, &newSecret); err != nil {
		return jucv1alpha1.Secret{}, err
	}
	expireAt := ""
	if jwkSpec.ExpireDurationHours != 0 {
		expireAt = time.Now().Add(
			time.Duration(jwkSpec.ExpireDurationHours) * time.Hour,
		).Format(parseTimeLayout)
	}
	return jucv1alpha1.Secret{
		SecretRef: corev1.ObjectReference{
			Namespace: objMeta.Namespace,
			Name:      secretName,
		},
		ExpireAt: expireAt,
	}, nil
}

func generatePrivateKey(jwkSpec jucv1alpha1.JWKSpec) (interface{}, error) {
	switch jwkSpec.KeyType {
	case "EC":
		return generateECPrivateKey(jwkSpec.CurveParameter)
	case "RSA":
		return generateRSAPrivateKey(jwkSpec.RSABitLength)
	default:
		return nil, errors.Errorf("unknown keyt type: %s", jwkSpec.KeyType)
	}
}

func generateECPrivateKey(curveParameter string) (interface{}, error) {
	switch curveParameter {
	case "P256", "P-256":
		return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	case "P384", "P-384":
		return ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	case "P521", "P-521":
		return ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	case "Ed25519":
		_, pri, err := ed25519.GenerateKey(rand.Reader)
		return pri, err
	default:
		return nil, errors.Errorf("Unknown Elliptic Curve: %s", curveParameter)
	}
}

func generateRSAPrivateKey(bits int32) (interface{}, error) {
	if bits < 512 || bits > 8192 {
		return nil, errors.Errorf("Invalid RSA key size: %d", bits)
	}
	return rsa.GenerateKey(rand.Reader, int(bits))
}

func (r *JWKReconciler) deleteJWKSecret(ctx context.Context, jwkSecret jucv1alpha1.Secret) error {
	secret := corev1.Secret{}
	nsn := types.NamespacedName{
		Namespace: jwkSecret.SecretRef.Namespace,
		Name:      jwkSecret.SecretRef.Name,
	}
	if err := r.Client.Get(ctx, nsn, &secret); err == nil {
		if err := r.Client.Delete(ctx, &secret); err != nil {
			return err
		}
	}
	return nil
}

func unsetJWK(s []jucv1alpha1.Secret, i int) []jucv1alpha1.Secret {
	if i >= len(s) {
		return s
	}
	return append(s[:i], s[i+1:]...)
}
