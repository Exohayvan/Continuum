package accounts

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

const testNodeID = "node-123"

func TestGetOrCreateKeysCreatesAndPersistsKeypair(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	storedFiles := map[string][]byte{}
	resolveNodeID = func() string { return testNodeID }
	readKeyFile = func(path string) ([]byte, error) {
		data, ok := storedFiles[path]
		if !ok {
			return nil, os.ErrNotExist
		}
		return append([]byte(nil), data...), nil
	}
	writeKeyFile = func(path string, data []byte, perm os.FileMode) error {
		if perm != privateKeyPerm {
			t.Fatalf("writeKeyFile() perm = %#o, want %#o", perm, privateKeyPerm)
		}
		storedFiles[path] = append([]byte(nil), data...)
		return nil
	}
	generateKeyPair = ed25519.GenerateKey

	first, err := GetOrCreateKeys()
	if err != nil {
		t.Fatalf("GetOrCreateKeys() error = %v", err)
	}
	if first.NodeID != testNodeID {
		t.Fatalf("GetOrCreateKeys().NodeID = %q, want %q", first.NodeID, testNodeID)
	}
	if first.KeyPath != filepath.Join(localAccountDir, testNodeID+keyFileSuffix) {
		t.Fatalf("GetOrCreateKeys().KeyPath = %q, want %q", first.KeyPath, filepath.Join(localAccountDir, testNodeID+keyFileSuffix))
	}
	if len(first.PrivateKey) != ed25519.PrivateKeySize {
		t.Fatalf("len(GetOrCreateKeys().PrivateKey) = %d, want %d", len(first.PrivateKey), ed25519.PrivateKeySize)
	}
	if len(first.PublicKey) != ed25519.PublicKeySize {
		t.Fatalf("len(GetOrCreateKeys().PublicKey) = %d, want %d", len(first.PublicKey), ed25519.PublicKeySize)
	}

	second, err := GetOrCreateKeys()
	if err != nil {
		t.Fatalf("GetOrCreateKeys() second call error = %v", err)
	}
	if string(second.PrivateKey) != string(first.PrivateKey) {
		t.Fatal("GetOrCreateKeys() did not reload the same private key")
	}
	if string(second.PublicKey) != string(first.PublicKey) {
		t.Fatal("GetOrCreateKeys() did not reload the same public key")
	}
}

func TestGetOrCreateKeysReturnsNodeIDError(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	resolveNodeID = func() string { return "" }

	if _, err := GetOrCreateKeys(); err == nil {
		t.Fatal("GetOrCreateKeys() error = nil, want node id failure")
	}
}

func TestGetOrCreateKeysReturnsReadError(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	wantErr := errors.New("read failed")
	resolveNodeID = func() string { return testNodeID }
	readKeyFile = func(string) ([]byte, error) {
		return nil, wantErr
	}

	if _, err := GetOrCreateKeys(); !errors.Is(err, wantErr) {
		t.Fatalf("GetOrCreateKeys() error = %v, want %v", err, wantErr)
	}
}

func TestGetOrCreateKeysReturnsDecodeError(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	resolveNodeID = func() string { return testNodeID }
	readKeyFile = func(string) ([]byte, error) {
		return []byte("not-base64"), nil
	}

	if _, err := GetOrCreateKeys(); err == nil {
		t.Fatal("GetOrCreateKeys() error = nil, want decode failure")
	}
}

func TestGetOrCreateKeysReturnsGenerateError(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	wantErr := errors.New("generate failed")
	resolveNodeID = func() string { return testNodeID }
	readKeyFile = func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	generateKeyPair = func(io.Reader) (ed25519.PublicKey, ed25519.PrivateKey, error) {
		return nil, nil, wantErr
	}

	if _, err := GetOrCreateKeys(); !errors.Is(err, wantErr) {
		t.Fatalf("GetOrCreateKeys() error = %v, want %v", err, wantErr)
	}
}

func TestGetOrCreateKeysReturnsWriteError(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	wantErr := errors.New("write failed")
	resolveNodeID = func() string { return testNodeID }
	readKeyFile = func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	generateKeyPair = ed25519.GenerateKey
	writeKeyFile = func(string, []byte, os.FileMode) error {
		return wantErr
	}

	if _, err := GetOrCreateKeys(); !errors.Is(err, wantErr) {
		t.Fatalf("GetOrCreateKeys() error = %v, want %v", err, wantErr)
	}
}

func TestBuildAndVerifyAccountTrustFiles(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	accountID := AccountIDFromPublicKey(publicKey)

	pubkeyData := BuildPublicKeyFile(publicKey)
	decodedPubKey, err := VerifyPublicKeyFile(accountID, pubkeyData)
	if err != nil {
		t.Fatalf("VerifyPublicKeyFile() error = %v", err)
	}
	if string(decodedPubKey) != string(publicKey) {
		t.Fatal("VerifyPublicKeyFile() returned a different public key")
	}

	metaData, err := BuildMeta(accountID, publicKey, "2026-04-02T00:00:00Z", 1, privateKey)
	if err != nil {
		t.Fatalf("BuildMeta() error = %v", err)
	}
	meta, err := VerifyMeta(accountID, publicKey, metaData)
	if err != nil {
		t.Fatalf("VerifyMeta() error = %v", err)
	}
	if meta.AccountID != accountID {
		t.Fatalf("VerifyMeta().AccountID = %q, want %q", meta.AccountID, accountID)
	}
}

func TestValidateBlobRejectsMismatchedAccountID(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	blobData, err := BuildBlob(AccountIDFromPublicKey(publicKey), publicKey, privateKey, "secret-pass")
	if err != nil {
		t.Fatalf("BuildBlob() error = %v", err)
	}

	var blob Blob
	if err := json.Unmarshal(blobData, &blob); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	blob.AccountID = "wrong-account"
	tamperedBlobData, err := json.Marshal(blob)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if _, _, err := ValidateBlob(tamperedBlobData); err == nil {
		t.Fatal("ValidateBlob() error = nil, want account id mismatch")
	}
}

func stubAccountHooks(t *testing.T) func() {
	t.Helper()

	originalResolveNodeID := resolveNodeID
	originalReadKeyFile := readKeyFile
	originalWriteKeyFile := writeKeyFile
	originalGenerateKeyPair := generateKeyPair

	resolveNodeID = func() string { return testNodeID }
	readKeyFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	writeKeyFile = func(string, []byte, os.FileMode) error { return nil }
	generateKeyPair = func(io.Reader) (ed25519.PublicKey, ed25519.PrivateKey, error) {
		return ed25519.GenerateKey(rand.Reader)
	}

	return func() {
		resolveNodeID = originalResolveNodeID
		readKeyFile = originalReadKeyFile
		writeKeyFile = originalWriteKeyFile
		generateKeyPair = originalGenerateKeyPair
	}
}
