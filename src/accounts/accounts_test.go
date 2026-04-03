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

const (
	testNodeID             = "node-123"
	testPassword           = "secret-pass"
	testSpacedAccountID    = " account-123 "
	writePermFormat        = "writeKeyFile() perm = %#o, want %#o"
	getOrCreateErrorFormat = "GetOrCreateKeys() error = %v, want %v"
	generateKeyErrorFormat = "GenerateKey() error = %v"
)

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
			t.Fatalf(writePermFormat, perm, privateKeyPerm)
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
		t.Fatalf(getOrCreateErrorFormat, err, wantErr)
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
		t.Fatalf(getOrCreateErrorFormat, err, wantErr)
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
		t.Fatalf(getOrCreateErrorFormat, err, wantErr)
	}
}

func TestGenerateCreatesAccountMaterial(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	storedFiles := map[string][]byte{}
	writeKeyFile = func(path string, data []byte, perm os.FileMode) error {
		if perm != privateKeyPerm {
			t.Fatalf(writePermFormat, perm, privateKeyPerm)
		}
		storedFiles[path] = append([]byte(nil), data...)
		return nil
	}

	material, err := Generate(testPassword)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if material.AccountID != AccountIDFromPublicKey(material.PublicKey) {
		t.Fatalf("Generate().AccountID = %q, want derived public key hash", material.AccountID)
	}
	if material.LocalKeyPath != filepath.Join(localAccountDir, material.AccountID+keyFileSuffix) {
		t.Fatalf("Generate().LocalKeyPath = %q, want %q", material.LocalKeyPath, filepath.Join(localAccountDir, material.AccountID+keyFileSuffix))
	}
	if _, ok := storedFiles[material.LocalKeyPath]; !ok {
		t.Fatal("Generate() did not persist the local private key")
	}

	blob, blobPublicKey, err := ValidateBlob(material.BlobData)
	if err != nil {
		t.Fatalf("ValidateBlob() error = %v", err)
	}
	if blob.AccountID != material.AccountID {
		t.Fatalf("ValidateBlob().AccountID = %q, want %q", blob.AccountID, material.AccountID)
	}
	if string(blobPublicKey) != string(material.PublicKey) {
		t.Fatal("ValidateBlob() returned a different public key")
	}
}

func TestRecoverRestoresAccountMaterial(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	accountID := AccountIDFromPublicKey(publicKey)
	blobData, err := BuildBlob(accountID, publicKey, privateKey, testPassword)
	if err != nil {
		t.Fatalf("BuildBlob() error = %v", err)
	}

	storedFiles := map[string][]byte{}
	writeKeyFile = func(path string, data []byte, perm os.FileMode) error {
		if perm != privateKeyPerm {
			t.Fatalf(writePermFormat, perm, privateKeyPerm)
		}
		storedFiles[path] = append([]byte(nil), data...)
		return nil
	}

	material, err := Recover(blobData, testPassword)
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if material.AccountID != accountID {
		t.Fatalf("Recover().AccountID = %q, want %q", material.AccountID, accountID)
	}
	if string(material.PublicKey) != string(publicKey) {
		t.Fatal("Recover() returned a different public key")
	}
	if string(material.PrivateKey) != string(privateKey) {
		t.Fatal("Recover() returned a different private key")
	}
	if _, ok := storedFiles[material.LocalKeyPath]; !ok {
		t.Fatal("Recover() did not persist the recovered private key")
	}
}

func TestSaveLocalKeyUsesDerivedPath(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}

	calledPath := ""
	writeKeyFile = func(path string, data []byte, perm os.FileMode) error {
		calledPath = path
		if perm != privateKeyPerm {
			t.Fatalf(writePermFormat, perm, privateKeyPerm)
		}
		if string(data) == "" {
			t.Fatal("writeKeyFile() data = empty, want encoded private key")
		}
		return nil
	}

	gotPath, err := SaveLocalKey(testSpacedAccountID, privateKey)
	if err != nil {
		t.Fatalf("SaveLocalKey() error = %v", err)
	}
	wantPath := filepath.Join(localAccountDir, "account-123"+keyFileSuffix)
	if gotPath != wantPath {
		t.Fatalf("SaveLocalKey() path = %q, want %q", gotPath, wantPath)
	}
	if calledPath != wantPath {
		t.Fatalf("writeKeyFile() path = %q, want %q", calledPath, wantPath)
	}
	if LocalKeyPath(testSpacedAccountID) != wantPath {
		t.Fatalf("LocalKeyPath() = %q, want %q", LocalKeyPath(testSpacedAccountID), wantPath)
	}
	if PubkeyFilePerm() != pubkeyFilePerm {
		t.Fatalf("PubkeyFilePerm() = %#o, want %#o", PubkeyFilePerm(), pubkeyFilePerm)
	}
}

func TestBuildAndVerifyAccountTrustFiles(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
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
		t.Fatalf(generateKeyErrorFormat, err)
	}
	blobData, err := BuildBlob(AccountIDFromPublicKey(publicKey), publicKey, privateKey, testPassword)
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
