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
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"

	autheliav1alpha1 "github.com/mnorrsken/heliop/api/v1alpha1"
)

// Keys of the core secret consumed by the Authelia container at /secrets.
const (
	keySessionEncryption = "SESSION_ENCRYPTION_KEY"
	keyStorageEncryption = "STORAGE_ENCRYPTION_KEY"
	keyOIDCHMACSecret    = "OIDC_HMAC_SECRET"
	keyOIDCPrivateKey    = "OIDC_PRIVATE_KEY"
)

const alphanumeric = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// pbkdf2Iterations matches Authelia's default for PBKDF2-SHA512.
const pbkdf2Iterations = 310000

// crypt64 is the "adapted base64" alphabet used by Authelia/go-crypt for PBKDF2
// digests: standard base64 with '+' replaced by '.', no padding.
var crypt64 = base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789./").WithPadding(base64.NoPadding)

// hashClientSecret hashes an OIDC client secret as PBKDF2-SHA512 in the digest
// format Authelia accepts (equivalent to
// `authelia crypto hash generate pbkdf2 --variant sha512`). PBKDF2 is the
// recommended scheme for client secrets as they are verified on every token
// request.
func hashClientSecret(secret string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha512.New, secret, salt, pbkdf2Iterations, sha512.Size)
	if err != nil {
		return "", fmt.Errorf("deriving pbkdf2 key: %w", err)
	}
	return fmt.Sprintf("$pbkdf2-sha512$%d$%s$%s", pbkdf2Iterations, crypt64.EncodeToString(salt), crypt64.EncodeToString(key)), nil
}

// coreSecretName resolves the name of the Secret holding Authelia's core
// secrets: the user-provided existing Secret, or the operator-managed one.
func coreSecretName(a *autheliav1alpha1.Authelia) string {
	if a.Spec.Deployment.ExistingSecret != "" {
		return a.Spec.Deployment.ExistingSecret
	}
	return a.Name + "-secrets"
}

// generateAlphanumeric returns a cryptographically random alphanumeric string of
// length n. Authelia recommends alphanumeric values for the session, storage
// encryption and OIDC HMAC secrets (avoiding base64 punctuation).
func generateAlphanumeric(n int) (string, error) {
	buf := make([]byte, n)
	max := big.NewInt(int64(len(alphanumeric)))
	for i := range buf {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = alphanumeric[idx.Int64()]
	}
	return string(buf), nil
}

// generateRSAPrivateKeyPEM returns a new RSA private key in PKCS#1 PEM form,
// suitable for the Authelia OIDC issuer private key (RSA, >= 2048 bits).
func generateRSAPrivateKeyPEM() ([]byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, fmt.Errorf("generating RSA key: %w", err)
	}
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	return pem.EncodeToMemory(block), nil
}

// generatedCoreSecretData fills any missing core secret keys in data, generating
// values once. Existing values are preserved so secrets are never rotated.
func generatedCoreSecretData(data map[string][]byte) (map[string][]byte, error) {
	if data == nil {
		data = map[string][]byte{}
	}

	for _, key := range []string{keySessionEncryption, keyStorageEncryption, keyOIDCHMACSecret} {
		if len(data[key]) > 0 {
			continue
		}
		v, err := generateAlphanumeric(64)
		if err != nil {
			return nil, err
		}
		data[key] = []byte(v)
	}

	if len(data[keyOIDCPrivateKey]) == 0 {
		pemBytes, err := generateRSAPrivateKeyPEM()
		if err != nil {
			return nil, err
		}
		data[keyOIDCPrivateKey] = pemBytes
	}

	return data, nil
}
