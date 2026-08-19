package auth_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asherzj/financial_configuration_center/internal/platform/auth"
	"golang.org/x/sys/unix"
)

func TestLoadEd25519PublicKeyRingFromStrictPKIXPEM(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	publicA, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicB, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pathA := writeKeyFile(t, directory, "a.pem", publicKeyPEM(t, publicA), 0o600)
	pathB := writeKeyFile(t, directory, "b.pem", publicKeyPEM(t, publicB), 0o400)
	ring, err := auth.LoadEd25519PublicKeyRing(map[string]string{"key-b": pathB, "key-a": pathA})
	if err != nil {
		t.Fatal(err)
	}
	resolvedA, err := ring.Resolve(t.Context(), "key-a")
	if err != nil || !resolvedA.Equal(publicA) {
		t.Fatalf("resolved key-a = %x, %v", resolvedA, err)
	}
	resolvedA[0] ^= 0xff
	again, err := ring.Resolve(t.Context(), "key-a")
	if err != nil || !again.Equal(publicA) {
		t.Fatal("caller mutation changed the loaded key ring")
	}
}

func TestLoadEd25519PublicKeyRingFailsClosed(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	valid := publicKeyPEM(t, publicKey)
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	rsaDER, err := x509.MarshalPKIXPublicKey(&rsaKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		content []byte
		mode    os.FileMode
	}{
		{name: "not PEM", content: []byte("not-pem"), mode: 0o600},
		{name: "wrong PEM type", content: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: valid}), mode: 0o600},
		{name: "RSA key", content: pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: rsaDER}), mode: 0o600},
		{name: "multiple blocks", content: append(append([]byte(nil), valid...), valid...), mode: 0o600},
		{name: "oversized", content: make([]byte, (16<<10)+1), mode: 0o600},
		{name: "group writable", content: valid, mode: 0o620},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := writeKeyFile(t, directory, strings.ReplaceAll(test.name, " ", "-")+".pem", test.content, test.mode)
			if _, err := auth.LoadEd25519PublicKeyRing(map[string]string{"key": path}); err == nil {
				t.Fatal("invalid public key ring accepted")
			}
		})
	}
	if _, err := auth.LoadEd25519PublicKeyRing(map[string]string{}); err == nil {
		t.Fatal("empty public key ring accepted")
	}
	secretPath := filepath.Join(directory, "do-not-log.pem")
	if _, err := auth.LoadEd25519PublicKeyRing(map[string]string{"key": secretPath}); err == nil || strings.Contains(err.Error(), secretPath) {
		t.Fatalf("missing key error leaked mounted path: %v", err)
	}
}

func TestLoadEd25519PublicKeyRingRejectsSpecialFileWithoutBlocking(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "key.fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := auth.LoadEd25519PublicKeyRing(map[string]string{"key": path})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO public key file accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO public key file blocked startup")
	}
}

func TestLoadEd25519PublicKeyRingSupportsMountedSymlinkAndValidatesFinalInode(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	target := writeKeyFile(t, directory, "..data-key.pem", publicKeyPEM(t, publicKey), 0o444)
	link := filepath.Join(directory, "current.pem")
	if err := os.Symlink(filepath.Base(target), link); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.LoadEd25519PublicKeyRing(map[string]string{"key": link}); err != nil {
		t.Fatalf("read-only projected-secret symlink was rejected: %v", err)
	}
	writableTarget := writeKeyFile(t, directory, "..data-writable.pem", publicKeyPEM(t, publicKey), 0o620)
	writableLink := filepath.Join(directory, "writable.pem")
	if err := os.Symlink(filepath.Base(writableTarget), writableLink); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.LoadEd25519PublicKeyRing(map[string]string{"key": writableLink}); err == nil {
		t.Fatal("symlink bypassed final-inode public key permission checks")
	}
}

func writeKeyFile(t *testing.T, directory, name string, contents []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func publicKeyPEM(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}
