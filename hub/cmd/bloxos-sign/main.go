package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bokiko/bloxos/proto/updatesigning"
)

const keyEnv = "BLOXOS_UPDATE_SIGNING_KEY"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "bloxos-sign: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("bloxos-sign", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	osName := fs.String("os", "", "target operating system: linux or windows")
	keyPath := fs.String("key", "", "Ed25519 private key path")
	printPublicKey := fs.Bool("print-public-key", false, "print the matching base64 public key and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	resolvedKey, err := resolveKeyPath(*keyPath)
	if err != nil {
		return err
	}
	if *printPublicKey {
		if fs.NArg() != 0 || strings.TrimSpace(*osName) != "" {
			return fmt.Errorf("-print-public-key does not accept -os or a binary path")
		}
		pub, err := publicKeyFor(resolvedKey)
		if err != nil {
			return err
		}
		fmt.Fprintln(stdout, base64.StdEncoding.EncodeToString(pub))
		return nil
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: bloxos-sign -os <linux|windows> [-key path] <agent-binary>")
	}

	result, err := updatesigning.SignFile(fs.Arg(0), *osName, resolvedKey)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "signed os=%s sha256=%s signature=%s\n",
		strings.ToLower(strings.TrimSpace(*osName)), result.SHA256, result.SignaturePath)
	return nil
}

func resolveKeyPath(flagValue string) (string, error) {
	if path := strings.TrimSpace(flagValue); path != "" {
		return path, nil
	}
	if path := strings.TrimSpace(os.Getenv(keyEnv)); path != "" {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve default signing key path: %w", err)
	}
	return filepath.Join(home, ".bloxos", "update-signing.key"), nil
}

func publicKeyFor(keyPath string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key %s: %w", keyPath, err)
	}
	priv, err := updatesigning.DecodePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("decode private key %s: %w", keyPath, err)
	}
	return priv.Public().(ed25519.PublicKey), nil
}
