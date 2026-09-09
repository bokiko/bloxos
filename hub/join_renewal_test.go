package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the real copied command across renewal, not just a config string.
// Renewing the certificate with the same key must work; changing the key must
// still fail closed, even when the response contains an executable script.
func TestJoinCommandAcrossCertificateRenewal(t *testing.T) {
	for _, tool := range []string{"bash", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s unavailable", tool)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := func(serial int64, key *ecdsa.PrivateKey) *tls.Certificate {
		t.Helper()
		template := &x509.Certificate{
			SerialNumber: big.NewInt(serial), NotBefore: time.Now().Add(-time.Minute),
			NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	var current atomic.Pointer[tls.Certificate]
	initial := certificate(1, key)
	current.Store(initial)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("echo bloxos-renewal-script-ran\n"))
	}))
	srv.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return &tls.Config{Certificates: []tls.Certificate{*current.Load()}}, nil
	}}
	srv.StartTLS()
	defer srv.Close()
	leaf, err := x509.ParseCertificate(initial.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	command := buildLinuxJoinCommand(srv.URL+"/api/join/test", spkiPinOf(leaf))
	for _, serial := range []int64{1, 2} {
		current.Store(certificate(serial, key))
		out, err := exec.Command("bash", "-c", command).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "bloxos-renewal-script-ran") {
			t.Fatalf("certificate %d: %v, %s", serial, err, out)
		}
	}
	newKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	current.Store(certificate(3, newKey))
	out, err := exec.Command("bash", "-c", command).CombinedOutput()
	if err == nil || strings.Contains(string(out), "bloxos-renewal-script-ran") {
		t.Fatalf("key rotation bypassed pin: %v, %s", err, out)
	}
}
