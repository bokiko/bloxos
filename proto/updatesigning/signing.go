// Package updatesigning defines the canonical BloxOS agent-update signature
// format and the helpers used to create and verify detached release signatures.
package updatesigning

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Context namespaces agent-update signatures from every other use of the key.
const Context = "bloxos-agent-update:v1"

// Result describes a detached signature written by SignFile.
type Result struct {
	SHA256        string
	Signature     string
	SignaturePath string
}

// Message returns the exact bytes signed for an agent release.
func Message(osName, sha256hex string) []byte {
	return []byte(Context + ":" +
		strings.ToLower(strings.TrimSpace(osName)) + ":" +
		strings.ToLower(strings.TrimSpace(sha256hex)))
}

// DecodePublicKey parses the first non-empty, non-comment base64 Ed25519 public key.
func DecodePublicKey(data []byte) (ed25519.PublicKey, error) {
	raw, err := decodeFirstLine(data)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("key is %d bytes, want %d", len(raw), ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// DecodePrivateKey parses the first non-empty, non-comment base64 Ed25519
// private key. Both a full private key and a seed are accepted.
func DecodePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	raw, err := decodeFirstLine(data)
	if err != nil {
		return nil, err
	}
	switch len(raw) {
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	default:
		return nil, fmt.Errorf("key is %d bytes, want %d (private) or %d (seed)",
			len(raw), ed25519.PrivateKeySize, ed25519.SeedSize)
	}
}

func decodeFirstLine(data []byte) ([]byte, error) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(line)
		if err != nil {
			return nil, fmt.Errorf("not valid base64: %w", err)
		}
		return raw, nil
	}
	return nil, fmt.Errorf("no key found in file")
}

// Verify checks a base64 Ed25519 signature for the supplied OS and SHA.
func Verify(pub ed25519.PublicKey, osName, sha256hex, sigB64 string) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("public key is malformed")
	}
	sha256hex = strings.TrimSpace(sha256hex)
	rawSHA, err := hex.DecodeString(sha256hex)
	if err != nil || len(rawSHA) != sha256.Size {
		return fmt.Errorf("SHA-256 must be exactly %d hex bytes", sha256.Size)
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil {
		return fmt.Errorf("signature is not valid base64: %w", err)
	}
	if len(sig) != ed25519.SignatureSize {
		return fmt.Errorf("signature is %d bytes, want %d", len(sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(pub, Message(osName, sha256hex), sig) {
		return fmt.Errorf("signature does not verify against the update key")
	}
	return nil
}

// SignFile hashes binaryPath, signs its canonical release message with the key
// at privateKeyPath, and atomically writes a base64 signature to <binary>.sig.
func SignFile(binaryPath, osName, privateKeyPath string) (Result, error) {
	osName = strings.ToLower(strings.TrimSpace(osName))
	if osName != "linux" && osName != "windows" {
		return Result{}, fmt.Errorf("unsupported OS %q: want linux or windows", osName)
	}
	if strings.TrimSpace(binaryPath) == "" {
		return Result{}, fmt.Errorf("binary path is required")
	}
	if strings.TrimSpace(privateKeyPath) == "" {
		return Result{}, fmt.Errorf("private key path is required")
	}

	keyData, err := os.ReadFile(privateKeyPath)
	if err != nil {
		return Result{}, fmt.Errorf("read private key %s: %w", privateKeyPath, err)
	}
	priv, err := DecodePrivateKey(keyData)
	if err != nil {
		return Result{}, fmt.Errorf("decode private key %s: %w", privateKeyPath, err)
	}

	f, err := os.Open(binaryPath)
	if err != nil {
		return Result{}, fmt.Errorf("open binary %s: %w", binaryPath, err)
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if copyErr != nil {
		return Result{}, fmt.Errorf("hash binary %s: %w", binaryPath, copyErr)
	}
	if closeErr != nil {
		return Result{}, fmt.Errorf("close binary %s: %w", binaryPath, closeErr)
	}
	sha := hex.EncodeToString(h.Sum(nil))
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, Message(osName, sha)))
	sigPath := binaryPath + ".sig"
	if err := writeSignatureAtomic(sigPath, sig+"\n"); err != nil {
		return Result{}, err
	}
	return Result{SHA256: sha, Signature: sig, SignaturePath: sigPath}, nil
}

func writeSignatureAtomic(path, body string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*")
	if err != nil {
		return fmt.Errorf("create temporary signature beside %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	removeTemp := true
	defer func() {
		if removeTemp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set signature permissions: %w", err)
	}
	if _, err := io.WriteString(tmp, body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write signature: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync signature: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close signature: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install signature %s: %w", path, err)
	}
	removeTemp = false
	return nil
}
