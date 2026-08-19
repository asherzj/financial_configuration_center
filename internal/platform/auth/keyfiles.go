package auth

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const maxPublicKeyFileBytes = 16 << 10

func LoadEd25519PublicKeyRing(files map[string]string) (StaticKeys, error) {
	if len(files) == 0 {
		return nil, errors.New("Internal JWT public key files are required")
	}
	keyIDs := make([]string, 0, len(files))
	for keyID := range files {
		keyIDs = append(keyIDs, keyID)
	}
	sort.Strings(keyIDs)
	keys := make(StaticKeys, len(files))
	for _, keyID := range keyIDs {
		contents, err := readBoundedPublicKeyFile(files[keyID])
		if err != nil {
			return nil, fmt.Errorf("load Internal JWT public key %q: %w", keyID, err)
		}
		key, err := parseEd25519PublicKeyPEM(contents)
		if err != nil {
			return nil, fmt.Errorf("load Internal JWT public key %q: %w", keyID, err)
		}
		keys[keyID] = key
	}
	return keys, nil
}

func readBoundedPublicKeyFile(path string) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("key file unavailable")
	}
	file := os.NewFile(uintptr(fd), "internal-jwt-public-key")
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("key file unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("key file must be a regular file")
	}
	if info.Size() <= 0 || info.Size() > maxPublicKeyFileBytes {
		return nil, errors.New("key file size is invalid")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("key file must not be group- or world-writable")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maxPublicKeyFileBytes+1))
	if err != nil || len(contents) == 0 || len(contents) > maxPublicKeyFileBytes {
		return nil, errors.New("key file bounded read failed")
	}
	return contents, nil
}

func parseEd25519PublicKeyPEM(contents []byte) (ed25519.PublicKey, error) {
	if len(contents) == 0 || len(contents) > maxPublicKeyFileBytes {
		return nil, errors.New("public key file size is invalid")
	}
	block, rest := pem.Decode(contents)
	if block == nil || block.Type != "PUBLIC KEY" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("public key file must contain exactly one PKIX PUBLIC KEY PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("public key file contains invalid PKIX data")
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("public key file must contain an Ed25519 public key")
	}
	return append(ed25519.PublicKey(nil), key...), nil
}
