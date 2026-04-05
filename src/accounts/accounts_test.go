package accounts

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/scrypt"
)

const (
	testNodeID             = "node-123"
	testAccountID          = "account-123"
	testOtherAccountID     = "other-account"
	testPassword           = "secret-pass"
	testSpacedAccountID    = " account-123 "
	testProfileUsername    = "Alice"
	testMetaCreatedAt      = "2026-04-02T00:00:00Z"
	testInvalidBase64      = "not-base64"
	testWriteFailed        = "write failed"
	testBuildBlobFormat    = "BuildBlob() error = %v"
	testBuildMetaFormat    = "BuildMeta() error = %v"
	testUnmarshalFormat    = "Unmarshal() error = %v"
	testSignPayloadFormat  = "signPayload() error = %v"
	testInvalidJSON        = "not-json"
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
		return []byte(testInvalidBase64), nil
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

	wantErr := errors.New(testWriteFailed)
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
		t.Fatalf(testBuildBlobFormat, err)
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

func TestUsernameHashAndLocalProfile(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	currentTime = func() time.Time {
		return time.Date(2026, 4, 4, 12, 0, 0, 0, time.UTC)
	}
	if UsernameHash(" Alice ") != UsernameHash("alice") {
		t.Fatal("UsernameHash() mismatch for normalized username")
	}

	profileData, err := BuildLocalProfile(testAccountID, " Alice ")
	if err != nil {
		t.Fatalf("BuildLocalProfile() error = %v", err)
	}
	var profile LocalProfile
	if err := json.Unmarshal(profileData, &profile); err != nil {
		t.Fatalf(testUnmarshalFormat, err)
	}
	if profile.AccountID != testAccountID || profile.Username != "Alice" {
		t.Fatalf("BuildLocalProfile() = %#v, want trimmed account/username", profile)
	}
	if profile.UsernameHash != UsernameHash("alice") {
		t.Fatalf("BuildLocalProfile().UsernameHash = %q, want normalized username hash", profile.UsernameHash)
	}
	if profile.UpdatedAt != "2026-04-04T12:00:00Z" {
		t.Fatalf("BuildLocalProfile().UpdatedAt = %q, want %q", profile.UpdatedAt, "2026-04-04T12:00:00Z")
	}
}

func TestBuildAndSaveLocalProfileReturnErrors(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	if _, err := BuildLocalProfile(" ", testProfileUsername); err == nil {
		t.Fatal("BuildLocalProfile() error = nil, want blank account id failure")
	}
	if _, err := BuildLocalProfile(testAccountID, " "); err == nil {
		t.Fatal("BuildLocalProfile() error = nil, want blank username failure")
	}
	if _, err := SaveLocalProfile(" ", testProfileUsername); err == nil {
		t.Fatal("SaveLocalProfile() error = nil, want blank account id failure")
	}
	if _, err := SaveLocalProfile(testAccountID, " "); err == nil {
		t.Fatal("SaveLocalProfile() error = nil, want blank username failure")
	}

	writtenPath := ""
	writeProfileFile = func(path string, data []byte, perm os.FileMode) error {
		writtenPath = path
		if perm != profileFilePerm {
			t.Fatalf("writeProfileFile() perm = %#o, want %#o", perm, profileFilePerm)
		}
		if !strings.Contains(string(data), `"username": "Alice"`) {
			t.Fatalf("writeProfileFile() data = %s, want profile username json", string(data))
		}
		return nil
	}

	gotPath, err := SaveLocalProfile(testAccountID, testProfileUsername)
	if err != nil {
		t.Fatalf("SaveLocalProfile() error = %v", err)
	}
	wantPath := filepath.Join(localAccountDir, testAccountID+profileSuffix)
	if gotPath != wantPath || writtenPath != wantPath || LocalProfilePath(testAccountID) != wantPath {
		t.Fatalf("SaveLocalProfile() path handling = (%q, %q, %q), want %q", gotPath, writtenPath, LocalProfilePath(testAccountID), wantPath)
	}

	wantErr := errors.New(testWriteFailed)
	writeProfileFile = func(string, []byte, os.FileMode) error { return wantErr }
	if _, err := SaveLocalProfile(testAccountID, testProfileUsername); !errors.Is(err, wantErr) {
		t.Fatalf("SaveLocalProfile() error = %v, want %v", err, wantErr)
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

	metaData, err := BuildMeta(accountID, publicKey, testMetaCreatedAt, 1, privateKey)
	if err != nil {
		t.Fatalf(testBuildMetaFormat, err)
	}
	meta, err := VerifyMeta(accountID, publicKey, metaData)
	if err != nil {
		t.Fatalf("VerifyMeta() error = %v", err)
	}
	if meta.AccountID != accountID {
		t.Fatalf("VerifyMeta().AccountID = %q, want %q", meta.AccountID, accountID)
	}
}

func TestBuildAndVerifyAccountMetaWithUsernameHash(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	accountID := AccountIDFromPublicKey(publicKey)

	metaData, err := BuildMetaWithUsernameHash(accountID, publicKey, testMetaCreatedAt, 1, UsernameHash("Alice"), privateKey)
	if err != nil {
		t.Fatalf(testBuildMetaFormat, err)
	}
	meta, err := VerifyMeta(accountID, publicKey, metaData)
	if err != nil {
		t.Fatalf("VerifyMeta() error = %v", err)
	}
	if meta.UsernameHash != UsernameHash("Alice") {
		t.Fatalf("VerifyMeta().UsernameHash = %q, want %q", meta.UsernameHash, UsernameHash("Alice"))
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
		t.Fatalf(testBuildBlobFormat, err)
	}

	var blob Blob
	if err := json.Unmarshal(blobData, &blob); err != nil {
		t.Fatalf(testUnmarshalFormat, err)
	}
	blob.AccountID = "wrong-account"
	unsigned := unsignedBlob{
		AccountID:  blob.AccountID,
		PublicKey:  blob.PublicKey,
		KDF:        blob.KDF,
		Salt:       blob.Salt,
		Nonce:      blob.Nonce,
		Ciphertext: blob.Ciphertext,
	}
	signature, err := signPayload(privateKey, unsigned)
	if err != nil {
		t.Fatalf(testSignPayloadFormat, err)
	}
	blob.Signature = signature
	tamperedBlobData, err := json.Marshal(blob)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	if _, _, err := ValidateBlob(tamperedBlobData); err == nil {
		t.Fatal("ValidateBlob() error = nil, want account id mismatch")
	}
}

func TestDecodePrivateKeyRejectsWrongLength(t *testing.T) {
	data := []byte(base64.StdEncoding.EncodeToString([]byte("short")))

	if _, err := decodePrivateKey(data); err == nil {
		t.Fatal("decodePrivateKey() error = nil, want length failure")
	}
}

func TestRequiredAccountIDAndValidateAccountPasswordRejectBlank(t *testing.T) {
	if _, err := requiredAccountID("   "); err == nil {
		t.Fatal("requiredAccountID() error = nil, want blank account id failure")
	}
	if err := validateAccountPassword(" \t "); err == nil {
		t.Fatal("validateAccountPassword() error = nil, want blank password failure")
	}
}

func TestSaveLocalKeyReturnsErrors(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}

	if _, err := SaveLocalKey("   ", privateKey); err == nil {
		t.Fatal("SaveLocalKey() error = nil, want blank account id failure")
	}

	wantErr := errors.New(testWriteFailed)
	writeKeyFile = func(string, []byte, os.FileMode) error {
		return wantErr
	}
	if _, err := SaveLocalKey(testAccountID, privateKey); !errors.Is(err, wantErr) {
		t.Fatalf("SaveLocalKey() error = %v, want %v", err, wantErr)
	}
}

func TestGenerateReturnsErrors(t *testing.T) {
	t.Run("password validation", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		if _, err := Generate(" "); err == nil {
			t.Fatal("Generate() error = nil, want password validation failure")
		}
	})

	t.Run("key generation", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		wantGenerateErr := errors.New("generate failed")
		generateKeyPair = func(io.Reader) (ed25519.PublicKey, ed25519.PrivateKey, error) {
			return nil, nil, wantGenerateErr
		}
		if _, err := Generate(testPassword); !errors.Is(err, wantGenerateErr) {
			t.Fatalf("Generate() error = %v, want %v", err, wantGenerateErr)
		}
	})

	t.Run("save local key", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		wantWriteErr := errors.New(testWriteFailed)
		writeKeyFile = func(string, []byte, os.FileMode) error {
			return wantWriteErr
		}
		if _, err := Generate(testPassword); !errors.Is(err, wantWriteErr) {
			t.Fatalf("Generate() error = %v, want %v", err, wantWriteErr)
		}
	})

	t.Run("build blob", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		randomReader = errReader{err: errors.New("salt read failed")}
		if _, err := Generate(testPassword); err == nil {
			t.Fatal("Generate() error = nil, want blob build failure")
		}
	})
}

func TestRecoverReturnsErrors(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	if _, err := Recover(nil, " "); err == nil {
		t.Fatal("Recover() error = nil, want password validation failure")
	}
	if _, err := Recover([]byte(testInvalidJSON), testPassword); err == nil {
		t.Fatal("Recover() error = nil, want blob validation failure")
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	accountID := AccountIDFromPublicKey(publicKey)

	mismatchBlobData := buildBlobWithEncryptedKey(t, accountID, publicKey, privateKey, mustGeneratePrivateKey(t), testPassword)
	if _, err := Recover(mismatchBlobData, testPassword); err == nil {
		t.Fatal("Recover() error = nil, want decrypted private key mismatch")
	}

	validBlobData, err := BuildBlob(accountID, publicKey, privateKey, testPassword)
	if err != nil {
		t.Fatalf(testBuildBlobFormat, err)
	}
	if _, err := Recover(validBlobData, "wrong-password"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("Recover() error = %v, want %v", err, ErrInvalidPassword)
	}

	wantWriteErr := errors.New(testWriteFailed)
	writeKeyFile = func(string, []byte, os.FileMode) error {
		return wantWriteErr
	}
	if _, err := Recover(validBlobData, testPassword); !errors.Is(err, wantWriteErr) {
		t.Fatalf("Recover() error = %v, want %v", err, wantWriteErr)
	}
}

func TestBuildBlobValidationErrors(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	accountID := AccountIDFromPublicKey(publicKey)

	t.Run("password validation", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		if _, err := BuildBlob(accountID, publicKey, privateKey, " "); err == nil {
			t.Fatal("BuildBlob() error = nil, want password validation failure")
		}
	})

	t.Run("account validation", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		if _, err := BuildBlob(" ", publicKey, privateKey, testPassword); err == nil {
			t.Fatal("BuildBlob() error = nil, want account id validation failure")
		}
	})

	t.Run("salt read", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		randomReader = errReader{err: errors.New("salt read failed")}
		if _, err := BuildBlob(accountID, publicKey, privateKey, testPassword); err == nil {
			t.Fatal("BuildBlob() error = nil, want salt read failure")
		}
	})
}

func TestBuildBlobConstructionErrors(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	accountID := AccountIDFromPublicKey(publicKey)

	t.Run("nonce read", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		randomReader = &scriptedReader{
			steps: []readStep{
				{count: blobSaltLength},
				{err: errors.New("nonce read failed")},
			},
		}
		if _, err := BuildBlob(accountID, publicKey, privateKey, testPassword); err == nil {
			t.Fatal("BuildBlob() error = nil, want nonce read failure")
		}
	})

	t.Run("derive key", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		deriveBlobKey = func(string, []byte) ([]byte, error) {
			return nil, errors.New("derive failed")
		}
		if _, err := BuildBlob(accountID, publicKey, privateKey, testPassword); err == nil {
			t.Fatal("BuildBlob() error = nil, want key derivation failure")
		}
	})

	t.Run("cipher", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		newAESCipher = func([]byte) (cipher.Block, error) {
			return nil, errors.New("cipher failed")
		}
		if _, err := BuildBlob(accountID, publicKey, privateKey, testPassword); err == nil {
			t.Fatal("BuildBlob() error = nil, want cipher creation failure")
		}
	})

	t.Run("gcm", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		newGCM = func(cipher.Block) (cipher.AEAD, error) {
			return nil, errors.New("gcm failed")
		}
		if _, err := BuildBlob(accountID, publicKey, privateKey, testPassword); err == nil {
			t.Fatal("BuildBlob() error = nil, want gcm creation failure")
		}
	})
}

func TestBuildBlobSignError(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	accountID := AccountIDFromPublicKey(publicKey)

	restore := stubAccountHooks(t)
	defer restore()

	signAccountPayload = func(ed25519.PrivateKey, any) (string, error) {
		return "", errors.New("sign failed")
	}
	if _, err := BuildBlob(accountID, publicKey, privateKey, testPassword); err == nil {
		t.Fatal("BuildBlob() error = nil, want sign failure")
	}
}

func TestDecodePublicKeyFileErrors(t *testing.T) {
	if _, err := DecodePublicKeyFile([]byte(testInvalidBase64)); err == nil {
		t.Fatal("DecodePublicKeyFile() error = nil, want decode failure")
	}
}

func TestBuildMetaDefaultsAndErrors(t *testing.T) {
	restore := stubAccountHooks(t)
	defer restore()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	accountID := AccountIDFromPublicKey(publicKey)

	if _, err := BuildMeta(" ", publicKey, "", 0, privateKey); err == nil {
		t.Fatal("BuildMeta() error = nil, want blank account id failure")
	}

	currentTime = func() time.Time {
		return time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	}
	metaData, err := BuildMeta(accountID, publicKey, "", 0, privateKey)
	if err != nil {
		t.Fatalf(testBuildMetaFormat, err)
	}
	var meta Meta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf(testUnmarshalFormat, err)
	}
	if meta.CreatedAt != "2026-04-03T12:00:00Z" || meta.Revision != 1 || meta.UpdatedAt != "2026-04-03T12:00:00Z" {
		t.Fatalf("BuildMeta() defaults = %#v, want default timestamp and revision", meta)
	}

	signAccountPayload = func(ed25519.PrivateKey, any) (string, error) {
		return "", errors.New("sign failed")
	}
	if _, err := BuildMeta(accountID, publicKey, testPassword, 1, privateKey); err == nil {
		t.Fatal("BuildMeta() error = nil, want sign failure")
	}
}

func TestVerifyMetaErrors(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	accountID := AccountIDFromPublicKey(publicKey)

	if _, err := VerifyMeta(accountID, publicKey, []byte(testInvalidJSON)); err == nil {
		t.Fatal("VerifyMeta() error = nil, want json failure")
	}

	metaData, err := BuildMeta(accountID, publicKey, testMetaCreatedAt, 1, privateKey)
	if err != nil {
		t.Fatalf(testBuildMetaFormat, err)
	}
	var meta Meta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf(testUnmarshalFormat, err)
	}

	if _, err := VerifyMeta(testOtherAccountID, publicKey, metaData); err == nil {
		t.Fatal("VerifyMeta() error = nil, want account mismatch")
	}

	meta.AccountID = testOtherAccountID
	accountMismatchData := mustMarshalJSON(t, meta)
	if _, err := VerifyMeta(accountID, publicKey, accountMismatchData); err == nil {
		t.Fatal("VerifyMeta() error = nil, want public key/account mismatch")
	}

	otherPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	if _, err := VerifyMeta(accountID, otherPublicKey, metaData); err == nil {
		t.Fatal("VerifyMeta() error = nil, want account/public key mismatch")
	}

	meta = mustDecodeMeta(t, metaData)
	meta.PublicKey = base64.StdEncoding.EncodeToString(mustGeneratePublicKey(t))
	publicKeyMismatchData := mustMarshalJSON(t, meta)
	if _, err := VerifyMeta(accountID, publicKey, publicKeyMismatchData); err == nil {
		t.Fatal("VerifyMeta() error = nil, want public key mismatch")
	}

	meta = mustDecodeMeta(t, metaData)
	meta.Revision = 0
	revisionData := mustMarshalJSON(t, meta)
	if _, err := VerifyMeta(accountID, publicKey, revisionData); err == nil {
		t.Fatal("VerifyMeta() error = nil, want revision failure")
	}

	meta = mustDecodeMeta(t, metaData)
	meta.Signature = "bad"
	badSignatureData := mustMarshalJSON(t, meta)
	if _, err := VerifyMeta(accountID, publicKey, badSignatureData); err == nil {
		t.Fatal("VerifyMeta() error = nil, want signature failure")
	}
}

func TestVerifyPublicKeyFileErrors(t *testing.T) {
	if _, err := VerifyPublicKeyFile(testAccountID, []byte(testInvalidBase64)); err == nil {
		t.Fatal("VerifyPublicKeyFile() error = nil, want decode failure")
	}

	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	if _, err := VerifyPublicKeyFile("wrong-account", BuildPublicKeyFile(publicKey)); err == nil {
		t.Fatal("VerifyPublicKeyFile() error = nil, want account mismatch")
	}
}

func TestValidateBlobErrors(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	accountID := AccountIDFromPublicKey(publicKey)

	if _, _, err := ValidateBlob([]byte(testInvalidJSON)); err == nil {
		t.Fatal("ValidateBlob() error = nil, want json failure")
	}

	invalidPubKeyBlob := Blob{AccountID: accountID, PublicKey: testInvalidBase64}
	if _, _, err := ValidateBlob(mustMarshalJSON(t, invalidPubKeyBlob)); err == nil {
		t.Fatal("ValidateBlob() error = nil, want public key decode failure")
	}

	blobData, err := BuildBlob(accountID, publicKey, privateKey, testPassword)
	if err != nil {
		t.Fatalf(testBuildBlobFormat, err)
	}

	var blob Blob
	if err := json.Unmarshal(blobData, &blob); err != nil {
		t.Fatalf(testUnmarshalFormat, err)
	}

	blob.Signature = "bad"
	if _, _, err := ValidateBlob(mustMarshalJSON(t, blob)); err == nil {
		t.Fatal("ValidateBlob() error = nil, want signature failure")
	}

	blob = mustDecodeBlob(t, blobData)
	blob.KDF = "argon2id"
	unsigned := unsignedBlob{
		AccountID:  blob.AccountID,
		PublicKey:  blob.PublicKey,
		KDF:        blob.KDF,
		Salt:       blob.Salt,
		Nonce:      blob.Nonce,
		Ciphertext: blob.Ciphertext,
	}
	signature, err := signPayload(privateKey, unsigned)
	if err != nil {
		t.Fatalf(testSignPayloadFormat, err)
	}
	blob.Signature = signature
	if _, _, err := ValidateBlob(mustMarshalJSON(t, blob)); err == nil {
		t.Fatal("ValidateBlob() error = nil, want unsupported kdf failure")
	}
}

func TestDecryptBlobPrivateKeyErrors(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	accountID := AccountIDFromPublicKey(publicKey)
	blobData, err := BuildBlob(accountID, publicKey, privateKey, testPassword)
	if err != nil {
		t.Fatalf(testBuildBlobFormat, err)
	}
	blob := mustDecodeBlob(t, blobData)

	invalidSalt := blob
	invalidSalt.Salt = testInvalidBase64
	if _, err := decryptBlobPrivateKey(invalidSalt, testPassword); err == nil {
		t.Fatal("decryptBlobPrivateKey() error = nil, want salt decode failure")
	}

	invalidNonce := blob
	invalidNonce.Nonce = testInvalidBase64
	if _, err := decryptBlobPrivateKey(invalidNonce, testPassword); err == nil {
		t.Fatal("decryptBlobPrivateKey() error = nil, want nonce decode failure")
	}

	invalidCiphertext := blob
	invalidCiphertext.Ciphertext = testInvalidBase64
	if _, err := decryptBlobPrivateKey(invalidCiphertext, testPassword); err == nil {
		t.Fatal("decryptBlobPrivateKey() error = nil, want ciphertext decode failure")
	}

	t.Run("derive key", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		deriveBlobKey = func(string, []byte) ([]byte, error) {
			return nil, errors.New("derive failed")
		}
		if _, err := decryptBlobPrivateKey(blob, testPassword); err == nil {
			t.Fatal("decryptBlobPrivateKey() error = nil, want key derivation failure")
		}
	})

	t.Run("cipher", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		newAESCipher = func([]byte) (cipher.Block, error) {
			return nil, errors.New("cipher failed")
		}
		if _, err := decryptBlobPrivateKey(blob, testPassword); err == nil {
			t.Fatal("decryptBlobPrivateKey() error = nil, want cipher creation failure")
		}
	})

	t.Run("gcm", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		newGCM = func(cipher.Block) (cipher.AEAD, error) {
			return nil, errors.New("gcm failed")
		}
		if _, err := decryptBlobPrivateKey(blob, testPassword); err == nil {
			t.Fatal("decryptBlobPrivateKey() error = nil, want gcm creation failure")
		}
	})

	if _, err := decryptBlobPrivateKey(blob, "wrong-password"); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("decryptBlobPrivateKey() error = %v, want %v", err, ErrInvalidPassword)
	}

	t.Run("decoded private key", func(t *testing.T) {
		restore := stubAccountHooks(t)
		defer restore()

		deriveBlobKey = func(string, []byte) ([]byte, error) {
			return make([]byte, blobKeyLength), nil
		}
		newAESCipher = func([]byte) (cipher.Block, error) {
			return fakeBlock{}, nil
		}
		newGCM = func(cipher.Block) (cipher.AEAD, error) {
			return fakeAEAD{openData: []byte("short")}, nil
		}
		if _, err := decryptBlobPrivateKey(blob, testPassword); err == nil {
			t.Fatal("decryptBlobPrivateKey() error = nil, want private key decode failure")
		}
	})
}

func TestSignAndVerifyHelpersReturnErrors(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}

	unsupportedPayload := make(chan int)
	if _, err := signPayload(make(ed25519.PrivateKey, ed25519.PrivateKeySize), unsupportedPayload); err == nil {
		t.Fatal("signPayload() error = nil, want marshal failure")
	}
	if err := verifySignature(publicKey, unsupportedPayload, ""); err == nil {
		t.Fatal("verifySignature() error = nil, want marshal failure")
	}
	if err := verifySignature(publicKey, map[string]string{"ok": "yes"}, testInvalidBase64); err == nil {
		t.Fatal("verifySignature() error = nil, want signature decode failure")
	}
	if err := verifySignature(publicKey, map[string]string{"ok": "yes"}, base64.StdEncoding.EncodeToString([]byte("bad"))); err == nil {
		t.Fatal("verifySignature() error = nil, want invalid signature failure")
	}
}

func TestDecodeFixedBase64Errors(t *testing.T) {
	if _, err := decodeFixedBase64(testInvalidBase64, 4); err == nil {
		t.Fatal("decodeFixedBase64() error = nil, want decode failure")
	}
	if _, err := decodeFixedBase64(base64.StdEncoding.EncodeToString([]byte("abc")), 4); err == nil {
		t.Fatal("decodeFixedBase64() error = nil, want length failure")
	}
}

func TestIOReadFullReturnsPartialError(t *testing.T) {
	buffer := make([]byte, 5)
	reader := &scriptedReader{
		steps: []readStep{
			{count: 3, err: errors.New("read failed")},
		},
	}

	total, err := ioReadFull(reader, buffer)
	if err == nil {
		t.Fatal("ioReadFull() error = nil, want read failure")
	}
	if total != 3 {
		t.Fatalf("ioReadFull() total = %d, want %d", total, 3)
	}
}

func stubAccountHooks(t *testing.T) func() {
	t.Helper()

	originalResolveNodeID := resolveNodeID
	originalReadKeyFile := readKeyFile
	originalWriteKeyFile := writeKeyFile
	originalWriteProfileFile := writeProfileFile
	originalGenerateKeyPair := generateKeyPair
	originalRandomReader := randomReader
	originalCurrentTime := currentTime
	originalDeriveBlobKey := deriveBlobKey
	originalNewAESCipher := newAESCipher
	originalNewGCM := newGCM
	originalSignAccountPayload := signAccountPayload

	resolveNodeID = func() string { return testNodeID }
	readKeyFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	writeKeyFile = func(string, []byte, os.FileMode) error { return nil }
	writeProfileFile = func(string, []byte, os.FileMode) error { return nil }
	generateKeyPair = func(io.Reader) (ed25519.PublicKey, ed25519.PrivateKey, error) {
		return ed25519.GenerateKey(rand.Reader)
	}
	randomReader = rand.Reader
	currentTime = time.Now
	deriveBlobKey = func(password string, salt []byte) ([]byte, error) {
		return scrypt.Key([]byte(password), salt, blobN, blobR, blobP, blobKeyLength)
	}
	newAESCipher = aes.NewCipher
	newGCM = cipher.NewGCM
	signAccountPayload = signPayload

	return func() {
		resolveNodeID = originalResolveNodeID
		readKeyFile = originalReadKeyFile
		writeKeyFile = originalWriteKeyFile
		writeProfileFile = originalWriteProfileFile
		generateKeyPair = originalGenerateKeyPair
		randomReader = originalRandomReader
		currentTime = originalCurrentTime
		deriveBlobKey = originalDeriveBlobKey
		newAESCipher = originalNewAESCipher
		newGCM = originalNewGCM
		signAccountPayload = originalSignAccountPayload
	}
}

type readStep struct {
	count int
	err   error
}

type scriptedReader struct {
	steps []readStep
	index int
}

func (r *scriptedReader) Read(buffer []byte) (int, error) {
	if r.index >= len(r.steps) {
		return 0, io.EOF
	}

	step := r.steps[r.index]
	r.index++
	for i := 0; i < step.count && i < len(buffer); i++ {
		buffer[i] = byte(i + 1)
	}

	return step.count, step.err
}

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
}

type fakeBlock struct{}

func (fakeBlock) BlockSize() int          { return aes.BlockSize }
func (fakeBlock) Encrypt(dst, src []byte) { copy(dst, src) } // Copy-only fake block for decrypt-path tests.
func (fakeBlock) Decrypt(dst, src []byte) { copy(dst, src) } // Copy-only fake block for decrypt-path tests.

type fakeAEAD struct {
	openData []byte
	openErr  error
}

func (f fakeAEAD) NonceSize() int { return blobNonceLength }
func (f fakeAEAD) Overhead() int  { return 0 }
func (f fakeAEAD) Seal(dst, nonce, plaintext, additionalData []byte) []byte {
	return append(dst, plaintext...)
}
func (f fakeAEAD) Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	return append(dst, f.openData...), nil
}

func mustGeneratePrivateKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	return privateKey
}

func mustGeneratePublicKey(t *testing.T) ed25519.PublicKey {
	t.Helper()

	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyErrorFormat, err)
	}
	return publicKey
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()

	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data
}

func mustDecodeBlob(t *testing.T, data []byte) Blob {
	t.Helper()

	var blob Blob
	if err := json.Unmarshal(data, &blob); err != nil {
		t.Fatalf(testUnmarshalFormat, err)
	}
	return blob
}

func mustDecodeMeta(t *testing.T, data []byte) Meta {
	t.Helper()

	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf(testUnmarshalFormat, err)
	}
	return meta
}

func buildBlobWithEncryptedKey(t *testing.T, accountID string, publicKey ed25519.PublicKey, signingKey ed25519.PrivateKey, encryptedKey ed25519.PrivateKey, password string) []byte {
	t.Helper()

	salt := make([]byte, blobSaltLength)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		t.Fatalf("io.ReadFull(salt) error = %v", err)
	}
	nonce := make([]byte, blobNonceLength)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		t.Fatalf("io.ReadFull(nonce) error = %v", err)
	}
	key, err := scrypt.Key([]byte(password), salt, blobN, blobR, blobP, blobKeyLength)
	if err != nil {
		t.Fatalf("scrypt.Key() error = %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher() error = %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM() error = %v", err)
	}
	unsigned := unsignedBlob{
		AccountID:  accountID,
		PublicKey:  base64.StdEncoding.EncodeToString(publicKey),
		KDF:        blobKDFName,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(gcm.Seal(nil, nonce, encodePrivateKey(encryptedKey), nil)),
	}
	signature, err := signPayload(signingKey, unsigned)
	if err != nil {
		t.Fatalf(testSignPayloadFormat, err)
	}

	return mustMarshalJSON(t, Blob{
		AccountID:  unsigned.AccountID,
		PublicKey:  unsigned.PublicKey,
		KDF:        unsigned.KDF,
		Salt:       unsigned.Salt,
		Nonce:      unsigned.Nonce,
		Ciphertext: unsigned.Ciphertext,
		Signature:  signature,
	})
}
