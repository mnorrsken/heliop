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
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func TestGeneratedCoreSecretData(t *testing.T) {
	data, err := generatedCoreSecretData(nil)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	for _, key := range []string{keySessionEncryption, keyStorageEncryption, keyOIDCHMACSecret} {
		if got := len(data[key]); got != 64 {
			t.Errorf("%s length = %d, want 64", key, got)
		}
	}

	block, _ := pem.Decode(data[keyOIDCPrivateKey])
	if block == nil {
		t.Fatal("OIDC_PRIVATE_KEY is not valid PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("OIDC_PRIVATE_KEY is not a valid PKCS#1 RSA key: %v", err)
	}
	if key.N.BitLen() < 2048 {
		t.Errorf("RSA key too small: %d bits", key.N.BitLen())
	}
}

func TestGeneratedCoreSecretDataPreservesExisting(t *testing.T) {
	existing := map[string][]byte{
		keySessionEncryption: []byte("keep-me"),
		keyOIDCPrivateKey:    []byte("existing-key"),
	}
	data, err := generatedCoreSecretData(existing)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if string(data[keySessionEncryption]) != "keep-me" {
		t.Error("existing session key was overwritten")
	}
	if string(data[keyOIDCPrivateKey]) != "existing-key" {
		t.Error("existing private key was overwritten")
	}
	if len(data[keyStorageEncryption]) != 64 {
		t.Error("missing storage key was not generated")
	}
}
