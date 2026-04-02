package accounts

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"continuum/src/datamanager"
	"continuum/src/nodeid"
	"golang.org/x/crypto/scrypt"
)

const (
	localAccountDir = "local/account"
	keyFileSuffix   = ".key"
	privateKeyPerm  = 0o600
	blobKeyLength   = 32
	blobSaltLength  = 16
	blobNonceLength = 12
	blobKDFName     = "scrypt"
	blobN           = 32768
	blobR           = 8
	blobP           = 1
	pubkeyFilePerm  = 0o644
)

var (
	resolveNodeID   = nodeid.GetNodeID
	readKeyFile     = datamanager.ReadFile
	writeKeyFile    = datamanager.WriteFile
	generateKeyPair = ed25519.GenerateKey
	randomReader    = rand.Reader
	currentTime     = time.Now
	deriveBlobKey   = func(password string, salt []byte) ([]byte, error) {
		return scrypt.Key([]byte(password), salt, blobN, blobR, blobP, blobKeyLength)
	}
	newAESCipher = aes.NewCipher
	newGCM       = cipher.NewGCM
)

var ErrInvalidPassword = errors.New("invalid account password")

type Blob struct {
	AccountID  string `json:"AccountID"`
	PublicKey  string `json:"PublicKey"`
	KDF        string `json:"KDF"`
	Salt       string `json:"Salt"`
	Nonce      string `json:"Nonce"`
	Ciphertext string `json:"Ciphertext"`
	Signature  string `json:"Signature"`
}

type unsignedBlob struct {
	AccountID  string `json:"AccountID"`
	PublicKey  string `json:"PublicKey"`
	KDF        string `json:"KDF"`
	Salt       string `json:"Salt"`
	Nonce      string `json:"Nonce"`
	Ciphertext string `json:"Ciphertext"`
}

type Meta struct {
	AccountID string `json:"AccountID"`
	PublicKey string `json:"PublicKey"`
	CreatedAt string `json:"Created At"`
	Revision  int    `json:"Revision"`
	UpdatedAt string `json:"Updated At"`
	Signature string `json:"Signature"`
}

type unsignedMeta struct {
	AccountID string `json:"AccountID"`
	PublicKey string `json:"PublicKey"`
	CreatedAt string `json:"Created At"`
	Revision  int    `json:"Revision"`
	UpdatedAt string `json:"Updated At"`
}

// Keys captures the persisted account signing keypair for the current node.
type Keys struct {
	NodeID     string
	KeyPath    string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// Material captures an account identity plus the local key path and encrypted
// blob payload used for network replication.
type Material struct {
	AccountID    string
	LocalKeyPath string
	PublicKey    ed25519.PublicKey
	PrivateKey   ed25519.PrivateKey
	BlobData     []byte
}

// GetOrCreateKeys loads the current node's persisted keypair, creating it on
// first use under data/local/account/<nodeID>.key.
func GetOrCreateKeys() (Keys, error) {
	nodeID := strings.TrimSpace(resolveNodeID())
	if nodeID == "" {
		return Keys{}, errors.New("unable to resolve node id for account keys")
	}

	keyPath := filepath.Join(localAccountDir, nodeID+keyFileSuffix)
	data, err := readKeyFile(keyPath)
	switch {
	case err == nil:
		privateKey, err := decodePrivateKey(data)
		if err != nil {
			return Keys{}, err
		}

		return Keys{
			NodeID:     nodeID,
			KeyPath:    keyPath,
			PublicKey:  privateKey.Public().(ed25519.PublicKey),
			PrivateKey: privateKey,
		}, nil
	case !errors.Is(err, os.ErrNotExist):
		return Keys{}, err
	}

	publicKey, privateKey, err := generateKeyPair(rand.Reader)
	if err != nil {
		return Keys{}, err
	}

	if err := writeKeyFile(keyPath, encodePrivateKey(privateKey), privateKeyPerm); err != nil {
		return Keys{}, err
	}

	return Keys{
		NodeID:     nodeID,
		KeyPath:    keyPath,
		PublicKey:  publicKey,
		PrivateKey: privateKey,
	}, nil
}

func encodePrivateKey(privateKey ed25519.PrivateKey) []byte {
	return []byte(base64.StdEncoding.EncodeToString(privateKey))
}

func decodePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	trimmedData := strings.TrimSpace(string(data))
	decoded, err := base64.StdEncoding.DecodeString(trimmedData)
	if err != nil {
		return nil, err
	}
	if len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid private key length: got %d, want %d", len(decoded), ed25519.PrivateKeySize)
	}

	return ed25519.PrivateKey(decoded), nil
}

// AccountIDFromPublicKey deterministically derives the account identifier from
// the public key so the same key always yields the same account id.
func AccountIDFromPublicKey(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}

// LocalKeyPath returns the managed-data relative path for the account's local
// private key copy.
func LocalKeyPath(accountID string) string {
	return filepath.Join(localAccountDir, strings.TrimSpace(accountID)+keyFileSuffix)
}

// SaveLocalKey persists the account private key in the managed local account
// directory using the account id as the filename.
func SaveLocalKey(accountID string, privateKey ed25519.PrivateKey) (string, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "", errors.New("account id is required")
	}

	path := LocalKeyPath(accountID)
	if err := writeKeyFile(path, encodePrivateKey(privateKey), privateKeyPerm); err != nil {
		return "", err
	}

	return path, nil
}

// Generate materializes a new account keypair, writes the local private key,
// and builds the encrypted blob payload protected by the supplied password.
func Generate(password string) (Material, error) {
	if strings.TrimSpace(password) == "" {
		return Material{}, errors.New("account password is required")
	}

	publicKey, privateKey, err := generateKeyPair(randomReader)
	if err != nil {
		return Material{}, err
	}

	accountID := AccountIDFromPublicKey(publicKey)
	localKeyPath, err := SaveLocalKey(accountID, privateKey)
	if err != nil {
		return Material{}, err
	}

	blobData, err := BuildBlob(accountID, publicKey, privateKey, password)
	if err != nil {
		return Material{}, err
	}

	return Material{
		AccountID:    accountID,
		LocalKeyPath: localKeyPath,
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
		BlobData:     blobData,
	}, nil
}

// Recover decrypts the provided account blob with the supplied password and
// stores the recovered private key locally under data/local/account/<accountID>.key.
func Recover(blobData []byte, password string) (Material, error) {
	if strings.TrimSpace(password) == "" {
		return Material{}, errors.New("account password is required")
	}

	var blob Blob
	if err := json.Unmarshal(blobData, &blob); err != nil {
		return Material{}, err
	}

	publicKey, err := decodeFixedBase64(blob.PublicKey, ed25519.PublicKeySize)
	if err != nil {
		return Material{}, err
	}

	unsigned := unsignedBlob{
		AccountID:  blob.AccountID,
		PublicKey:  blob.PublicKey,
		KDF:        blob.KDF,
		Salt:       blob.Salt,
		Nonce:      blob.Nonce,
		Ciphertext: blob.Ciphertext,
	}
	if err := verifySignature(ed25519.PublicKey(publicKey), unsigned, blob.Signature); err != nil {
		return Material{}, err
	}

	accountID := AccountIDFromPublicKey(ed25519.PublicKey(publicKey))
	if blob.AccountID != accountID {
		return Material{}, errors.New("account blob account id does not match public key")
	}
	if blob.KDF != blobKDFName {
		return Material{}, fmt.Errorf("unsupported account blob KDF: %s", blob.KDF)
	}

	salt, err := decodeFixedBase64(blob.Salt, blobSaltLength)
	if err != nil {
		return Material{}, err
	}
	nonce, err := decodeFixedBase64(blob.Nonce, blobNonceLength)
	if err != nil {
		return Material{}, err
	}
	ciphertext, err := base64.StdEncoding.DecodeString(strings.TrimSpace(blob.Ciphertext))
	if err != nil {
		return Material{}, err
	}

	key, err := deriveBlobKey(password, salt)
	if err != nil {
		return Material{}, err
	}
	block, err := newAESCipher(key)
	if err != nil {
		return Material{}, err
	}
	gcm, err := newGCM(block)
	if err != nil {
		return Material{}, err
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return Material{}, ErrInvalidPassword
	}

	privateKey, err := decodePrivateKey(plaintext)
	if err != nil {
		return Material{}, err
	}
	if !privateKey.Public().(ed25519.PublicKey).Equal(ed25519.PublicKey(publicKey)) {
		return Material{}, errors.New("account blob private key does not match public key")
	}

	localKeyPath, err := SaveLocalKey(accountID, privateKey)
	if err != nil {
		return Material{}, err
	}

	return Material{
		AccountID:    accountID,
		LocalKeyPath: localKeyPath,
		PublicKey:    ed25519.PublicKey(publicKey),
		PrivateKey:   privateKey,
		BlobData:     append([]byte(nil), blobData...),
	}, nil
}

// BuildBlob encrypts the private key with the supplied password and returns the
// canonical JSON payload stored as accountid.blob in the network data store.
func BuildBlob(accountID string, publicKey ed25519.PublicKey, privateKey ed25519.PrivateKey, password string) ([]byte, error) {
	if strings.TrimSpace(password) == "" {
		return nil, errors.New("account password is required")
	}
	if strings.TrimSpace(accountID) == "" {
		return nil, errors.New("account id is required")
	}

	salt := make([]byte, blobSaltLength)
	if _, err := ioReadFull(randomReader, salt); err != nil {
		return nil, err
	}
	nonce := make([]byte, blobNonceLength)
	if _, err := ioReadFull(randomReader, nonce); err != nil {
		return nil, err
	}

	key, err := deriveBlobKey(password, salt)
	if err != nil {
		return nil, err
	}
	block, err := newAESCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(block)
	if err != nil {
		return nil, err
	}

	ciphertext := gcm.Seal(nil, nonce, encodePrivateKey(privateKey), nil)
	unsigned := unsignedBlob{
		AccountID:  accountID,
		PublicKey:  base64.StdEncoding.EncodeToString(publicKey),
		KDF:        blobKDFName,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ciphertext),
	}
	signature, err := signPayload(privateKey, unsigned)
	if err != nil {
		return nil, err
	}

	return json.MarshalIndent(Blob{
		AccountID:  unsigned.AccountID,
		PublicKey:  unsigned.PublicKey,
		KDF:        unsigned.KDF,
		Salt:       unsigned.Salt,
		Nonce:      unsigned.Nonce,
		Ciphertext: unsigned.Ciphertext,
		Signature:  signature,
	}, "", "  ")
}

// BuildPublicKeyFile serializes the account public key into the on-disk
// accountID.pubkey payload.
func BuildPublicKeyFile(publicKey ed25519.PublicKey) []byte {
	return []byte(base64.StdEncoding.EncodeToString(publicKey))
}

// DecodePublicKeyFile parses the accountID.pubkey payload and returns the
// public key bytes.
func DecodePublicKeyFile(data []byte) (ed25519.PublicKey, error) {
	decoded, err := decodeFixedBase64(string(data), ed25519.PublicKeySize)
	if err != nil {
		return nil, err
	}

	return ed25519.PublicKey(decoded), nil
}

// BuildMeta constructs the signed accountID.meta payload.
func BuildMeta(accountID string, publicKey ed25519.PublicKey, createdAt string, revision int, privateKey ed25519.PrivateKey) ([]byte, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return nil, errors.New("account id is required")
	}
	if createdAt = strings.TrimSpace(createdAt); createdAt == "" {
		createdAt = currentTime().UTC().Format(time.RFC3339)
	}
	if revision <= 0 {
		revision = 1
	}

	unsigned := unsignedMeta{
		AccountID: accountID,
		PublicKey: base64.StdEncoding.EncodeToString(publicKey),
		CreatedAt: createdAt,
		Revision:  revision,
		UpdatedAt: currentTime().UTC().Format(time.RFC3339),
	}
	signature, err := signPayload(privateKey, unsigned)
	if err != nil {
		return nil, err
	}

	return json.MarshalIndent(Meta{
		AccountID: unsigned.AccountID,
		PublicKey: unsigned.PublicKey,
		CreatedAt: unsigned.CreatedAt,
		Revision:  unsigned.Revision,
		UpdatedAt: unsigned.UpdatedAt,
		Signature: signature,
	}, "", "  ")
}

// VerifyMeta validates the accountID.meta payload against the supplied public
// key bytes and returns the decoded metadata.
func VerifyMeta(accountID string, publicKey ed25519.PublicKey, data []byte) (Meta, error) {
	var meta Meta
	if err := json.Unmarshal(data, &meta); err != nil {
		return Meta{}, err
	}
	if strings.TrimSpace(meta.AccountID) != strings.TrimSpace(accountID) {
		return Meta{}, errors.New("account meta account id does not match expected account")
	}
	if meta.AccountID != AccountIDFromPublicKey(publicKey) {
		return Meta{}, errors.New("account meta account id does not match public key")
	}
	if meta.PublicKey != base64.StdEncoding.EncodeToString(publicKey) {
		return Meta{}, errors.New("account meta public key does not match account pubkey")
	}
	if meta.Revision <= 0 {
		return Meta{}, errors.New("account meta revision must be positive")
	}

	unsigned := unsignedMeta{
		AccountID: meta.AccountID,
		PublicKey: meta.PublicKey,
		CreatedAt: meta.CreatedAt,
		Revision:  meta.Revision,
		UpdatedAt: meta.UpdatedAt,
	}
	if err := verifySignature(publicKey, unsigned, meta.Signature); err != nil {
		return Meta{}, err
	}

	return meta, nil
}

// VerifyPublicKeyFile validates the accountID.pubkey payload against the
// account id and returns the decoded public key.
func VerifyPublicKeyFile(accountID string, data []byte) (ed25519.PublicKey, error) {
	publicKey, err := DecodePublicKeyFile(data)
	if err != nil {
		return nil, err
	}
	if AccountIDFromPublicKey(publicKey) != strings.TrimSpace(accountID) {
		return nil, errors.New("account pubkey does not match account id")
	}

	return publicKey, nil
}

// ValidateBlob verifies the signed account blob without decrypting it and
// returns the embedded account metadata.
func ValidateBlob(blobData []byte) (Blob, ed25519.PublicKey, error) {
	var blob Blob
	if err := json.Unmarshal(blobData, &blob); err != nil {
		return Blob{}, nil, err
	}

	publicKey, err := decodeFixedBase64(blob.PublicKey, ed25519.PublicKeySize)
	if err != nil {
		return Blob{}, nil, err
	}
	unsigned := unsignedBlob{
		AccountID:  blob.AccountID,
		PublicKey:  blob.PublicKey,
		KDF:        blob.KDF,
		Salt:       blob.Salt,
		Nonce:      blob.Nonce,
		Ciphertext: blob.Ciphertext,
	}
	if err := verifySignature(ed25519.PublicKey(publicKey), unsigned, blob.Signature); err != nil {
		return Blob{}, nil, err
	}
	if blob.AccountID != AccountIDFromPublicKey(ed25519.PublicKey(publicKey)) {
		return Blob{}, nil, errors.New("account blob account id does not match public key")
	}
	if blob.KDF != blobKDFName {
		return Blob{}, nil, fmt.Errorf("unsupported account blob KDF: %s", blob.KDF)
	}

	return blob, ed25519.PublicKey(publicKey), nil
}

// PubkeyFilePerm returns the file mode used for persisted account public key
// files in the network data store.
func PubkeyFilePerm() os.FileMode {
	return pubkeyFilePerm
}

func signPayload(privateKey ed25519.PrivateKey, payload any) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, data)), nil
}

func verifySignature(publicKey ed25519.PublicKey, payload any, signature string) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	decodedSignature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, data, decodedSignature) {
		return errors.New("account blob signature is invalid")
	}

	return nil
}

func decodeFixedBase64(value string, wantLength int) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, err
	}
	if len(decoded) != wantLength {
		return nil, fmt.Errorf("invalid decoded length: got %d, want %d", len(decoded), wantLength)
	}

	return decoded, nil
}

func ioReadFull(reader interface{ Read([]byte) (int, error) }, buffer []byte) (int, error) {
	total := 0
	for total < len(buffer) {
		n, err := reader.Read(buffer[total:])
		total += n
		if err != nil {
			return total, err
		}
	}

	return total, nil
}
