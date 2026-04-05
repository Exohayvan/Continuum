package networkmanager

import (
	"context"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

const (
	throughputWindow = time.Second
	bitsPerMegabit   = 1_000_000
	sessionProtocol  = "continuum-bootstrap/1"
	x25519KeyLength  = 32
)

var (
	currentTime  = time.Now
	managerState = trackedState{}
)

type transferEvent struct {
	recordedAt time.Time
	readBytes  uint64
	writeBytes uint64
}

type trackedState struct {
	mu              sync.Mutex
	totalReadBytes  uint64
	totalWriteBytes uint64
	transfers       []transferEvent
}

// Usage captures recent network throughput and lifetime transferred bytes.
type Usage struct {
	ReadMbps        float64 `json:"readMbps"`
	WriteMbps       float64 `json:"writeMbps"`
	TotalReadBytes  uint64  `json:"totalReadBytes"`
	TotalWriteBytes uint64  `json:"totalWriteBytes"`
}

type trackedReadWriter struct {
	target io.ReadWriter
}

type trackedReadWriteCloser struct {
	target io.ReadWriteCloser
}

type trackedConn struct {
	net.Conn
}

type trackedListener struct {
	net.Listener
}

type trackedSecureListener struct {
	net.Listener
	privateKey ed25519.PrivateKey
}

type secureConn struct {
	conn         net.Conn
	readAEAD     cipher.AEAD
	writeAEAD    cipher.AEAD
	readCounter  uint64
	writeCounter uint64
	readBuffer   []byte
	readMu       sync.Mutex
	writeMu      sync.Mutex
}

type secureClientHello struct {
	Protocol  string `json:"protocol"`
	PublicKey string `json:"publicKey"`
}

type secureServerHello struct {
	Protocol      string `json:"protocol"`
	AccountID     string `json:"accountId"`
	AccountPubKey string `json:"accountPubKey"`
	PublicKey     string `json:"publicKey"`
	Signature     string `json:"signature"`
}

type secureServerProof struct {
	Protocol        string `json:"protocol"`
	ClientPublicKey string `json:"clientPublicKey"`
	ServerPublicKey string `json:"serverPublicKey"`
	AccountID       string `json:"accountId"`
}

// Snapshot returns the current one-second throughput window plus lifetime totals.
func Snapshot() Usage {
	return managerState.snapshot(currentTime())
}

// DialTCP4 opens a tracked tcp4 connection.
func DialTCP4(ctx context.Context, host string, port int) (net.Conn, error) {
	conn, err := dialTCP4(ctx, host, port)
	if err != nil {
		return nil, err
	}

	return WrapConn(conn), nil
}

// ListenTCP4 opens a tracked tcp4 listener whose accepted connections are tracked.
func ListenTCP4(port int) (net.Listener, error) {
	listener, err := listenTCP4(port)
	if err != nil {
		return nil, err
	}

	return trackedListener{Listener: listener}, nil
}

// DialSecureTCP4 opens an authenticated encrypted tcp4 connection. When
// expectedAccountID is provided, the remote identity must hash to that account id.
func DialSecureTCP4(ctx context.Context, host string, port int, expectedAccountID string) (net.Conn, error) {
	rawConn, err := dialTCP4(ctx, host, port)
	if err != nil {
		return nil, err
	}

	conn := WrapConn(rawConn)
	secureConn, err := clientSecureConn(ctx, conn, expectedAccountID)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	return secureConn, nil
}

// ListenSecureTCP4 opens an authenticated encrypted tcp4 listener using the
// supplied ed25519 identity key.
func ListenSecureTCP4(port int, privateKey ed25519.PrivateKey) (net.Listener, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("bootstrap transport private key is invalid")
	}

	rawListener, err := listenTCP4(port)
	if err != nil {
		return nil, err
	}

	return trackedSecureListener{
		Listener:   rawListener,
		privateKey: append(ed25519.PrivateKey(nil), privateKey...),
	}, nil
}

// WrapReadWriter returns a wrapper that records bytes read and written.
func WrapReadWriter(target io.ReadWriter) io.ReadWriter {
	if target == nil {
		return nil
	}

	return trackedReadWriter{target: target}
}

// WrapReadWriteCloser returns a wrapper that records bytes read and written
// while preserving the wrapped Close behavior.
func WrapReadWriteCloser(target io.ReadWriteCloser) io.ReadWriteCloser {
	if target == nil {
		return nil
	}

	return trackedReadWriteCloser{target: target}
}

// WrapConn returns a net.Conn wrapper that records bytes read and written
// without changing any of the connection's deadline or addressing behavior.
func WrapConn(target net.Conn) net.Conn {
	if target == nil {
		return nil
	}

	return trackedConn{Conn: target}
}

func (w trackedReadWriter) Read(buffer []byte) (int, error) {
	count, err := w.target.Read(buffer)
	recordRead(count)
	return count, err
}

func (w trackedReadWriter) Write(buffer []byte) (int, error) {
	count, err := w.target.Write(buffer)
	recordWrite(count)
	return count, err
}

func (w trackedReadWriteCloser) Read(buffer []byte) (int, error) {
	count, err := w.target.Read(buffer)
	recordRead(count)
	return count, err
}

func (w trackedReadWriteCloser) Write(buffer []byte) (int, error) {
	count, err := w.target.Write(buffer)
	recordWrite(count)
	return count, err
}

func (w trackedReadWriteCloser) Close() error {
	return w.target.Close()
}

func (w trackedConn) Read(buffer []byte) (int, error) {
	count, err := w.Conn.Read(buffer)
	recordRead(count)
	return count, err
}

func (w trackedConn) Write(buffer []byte) (int, error) {
	count, err := w.Conn.Write(buffer)
	recordWrite(count)
	return count, err
}

func (w trackedListener) Accept() (net.Conn, error) {
	conn, err := w.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return WrapConn(conn), nil
}

func (w trackedSecureListener) Accept() (net.Conn, error) {
	for {
		conn, err := w.Listener.Accept()
		if err != nil {
			return nil, err
		}

		trackedConn := WrapConn(conn)
		secureConn, err := serverSecureConn(trackedConn, w.privateKey)
		if err != nil {
			_ = trackedConn.Close()
			continue
		}

		return secureConn, nil
	}
}

func dialTCP4(ctx context.Context, host string, port int) (net.Conn, error) {
	address := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	dialer := net.Dialer{}
	return dialer.DialContext(ctx, "tcp4", address)
}

func listenTCP4(port int) (net.Listener, error) {
	address := net.JoinHostPort("0.0.0.0", fmt.Sprintf("%d", port))
	return net.Listen("tcp4", address)
}

func accountIDFromPublicKey(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
}

func clientSecureConn(ctx context.Context, conn net.Conn, expectedAccountID string) (net.Conn, error) {
	curve := ecdh.X25519()
	clientPrivateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}

	clientPublicKey := clientPrivateKey.PublicKey().Bytes()
	if err := writeJSONMessage(conn, secureClientHello{
		Protocol:  sessionProtocol,
		PublicKey: base64.StdEncoding.EncodeToString(clientPublicKey),
	}); err != nil {
		return nil, err
	}

	var serverHello secureServerHello
	if err := readJSONMessage(conn, &serverHello); err != nil {
		return nil, err
	}
	if strings.TrimSpace(serverHello.Protocol) != sessionProtocol {
		return nil, errors.New("bootstrap transport protocol mismatch")
	}

	serverIdentityKey, err := decodeFixedBase64(serverHello.AccountPubKey, ed25519.PublicKeySize)
	if err != nil {
		return nil, err
	}
	accountID := accountIDFromPublicKey(ed25519.PublicKey(serverIdentityKey))
	if accountID != strings.TrimSpace(serverHello.AccountID) {
		return nil, errors.New("bootstrap transport account id does not match presented public key")
	}
	if expectedAccountID = strings.TrimSpace(expectedAccountID); expectedAccountID != "" && accountID != expectedAccountID {
		return nil, errors.New("bootstrap transport account id does not match expected bootstrap account")
	}

	serverPublicKeyBytes, err := decodeFixedBase64(serverHello.PublicKey, x25519KeyLength)
	if err != nil {
		return nil, err
	}
	serverPublicKey, err := curve.NewPublicKey(serverPublicKeyBytes)
	if err != nil {
		return nil, err
	}

	proof := secureServerProof{
		Protocol:        sessionProtocol,
		ClientPublicKey: base64.StdEncoding.EncodeToString(clientPublicKey),
		ServerPublicKey: serverHello.PublicKey,
		AccountID:       serverHello.AccountID,
	}
	if err := verifySignedProof(ed25519.PublicKey(serverIdentityKey), proof, serverHello.Signature); err != nil {
		return nil, err
	}

	sharedSecret, err := clientPrivateKey.ECDH(serverPublicKey)
	if err != nil {
		return nil, err
	}

	return newSecureConn(conn, sharedSecret, clientPublicKey, serverPublicKeyBytes, true)
}

func serverSecureConn(conn net.Conn, privateKey ed25519.PrivateKey) (net.Conn, error) {
	var clientHello secureClientHello
	if err := readJSONMessage(conn, &clientHello); err != nil {
		return nil, err
	}
	if strings.TrimSpace(clientHello.Protocol) != sessionProtocol {
		return nil, errors.New("bootstrap transport protocol mismatch")
	}

	curve := ecdh.X25519()
	clientPublicKeyBytes, err := decodeFixedBase64(clientHello.PublicKey, x25519KeyLength)
	if err != nil {
		return nil, err
	}
	clientPublicKey, err := curve.NewPublicKey(clientPublicKeyBytes)
	if err != nil {
		return nil, err
	}

	serverPrivateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	serverPublicKeyBytes := serverPrivateKey.PublicKey().Bytes()
	accountID := accountIDFromPublicKey(privateKey.Public().(ed25519.PublicKey))
	proof := secureServerProof{
		Protocol:        sessionProtocol,
		ClientPublicKey: clientHello.PublicKey,
		ServerPublicKey: base64.StdEncoding.EncodeToString(serverPublicKeyBytes),
		AccountID:       accountID,
	}
	signature, err := signProof(privateKey, proof)
	if err != nil {
		return nil, err
	}
	if err := writeJSONMessage(conn, secureServerHello{
		Protocol:      sessionProtocol,
		AccountID:     accountID,
		AccountPubKey: base64.StdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		PublicKey:     proof.ServerPublicKey,
		Signature:     signature,
	}); err != nil {
		return nil, err
	}

	sharedSecret, err := serverPrivateKey.ECDH(clientPublicKey)
	if err != nil {
		return nil, err
	}

	return newSecureConn(conn, sharedSecret, clientPublicKeyBytes, serverPublicKeyBytes, false)
}

func newSecureConn(conn net.Conn, sharedSecret, clientPublicKey, serverPublicKey []byte, clientRole bool) (net.Conn, error) {
	readKey, writeKey, err := deriveSessionKeys(sharedSecret, clientPublicKey, serverPublicKey, clientRole)
	if err != nil {
		return nil, err
	}

	readAEAD, err := chacha20poly1305.New(readKey)
	if err != nil {
		return nil, err
	}
	writeAEAD, err := chacha20poly1305.New(writeKey)
	if err != nil {
		return nil, err
	}

	return &secureConn{
		conn:      conn,
		readAEAD:  readAEAD,
		writeAEAD: writeAEAD,
	}, nil
}

func deriveSessionKeys(sharedSecret, clientPublicKey, serverPublicKey []byte, clientRole bool) ([]byte, []byte, error) {
	transcript := append(append([]byte(sessionProtocol), clientPublicKey...), serverPublicKey...)
	reader := hkdf.New(sha256.New, sharedSecret, transcript, []byte("continuum-bootstrap-transport"))

	clientWriteKey := make([]byte, chacha20poly1305.KeySize)
	serverWriteKey := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(reader, clientWriteKey); err != nil {
		return nil, nil, err
	}
	if _, err := io.ReadFull(reader, serverWriteKey); err != nil {
		return nil, nil, err
	}

	if clientRole {
		return serverWriteKey, clientWriteKey, nil
	}

	return clientWriteKey, serverWriteKey, nil
}

func (w *secureConn) Read(buffer []byte) (int, error) {
	w.readMu.Lock()
	defer w.readMu.Unlock()

	if len(buffer) == 0 {
		return 0, nil
	}
	for len(w.readBuffer) == 0 {
		if err := w.fillReadBuffer(); err != nil {
			return 0, err
		}
	}

	count := copy(buffer, w.readBuffer)
	w.readBuffer = w.readBuffer[count:]
	return count, nil
}

func (w *secureConn) Write(buffer []byte) (int, error) {
	w.writeMu.Lock()
	defer w.writeMu.Unlock()

	if len(buffer) == 0 {
		return 0, nil
	}

	nonce := counterNonce(w.writeCounter, w.writeAEAD.NonceSize())
	ciphertext := w.writeAEAD.Seal(nil, nonce, buffer, nil)
	frame := make([]byte, 4+len(ciphertext))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(ciphertext)))
	copy(frame[4:], ciphertext)
	if err := writeAll(w.conn, frame); err != nil {
		return 0, err
	}
	w.writeCounter++
	return len(buffer), nil
}

func (w *secureConn) fillReadBuffer() error {
	lengthPrefix := make([]byte, 4)
	if _, err := io.ReadFull(w.conn, lengthPrefix); err != nil {
		return err
	}

	ciphertextLength := binary.BigEndian.Uint32(lengthPrefix)
	if ciphertextLength == 0 {
		return errors.New("secure transport frame is empty")
	}

	ciphertext := make([]byte, ciphertextLength)
	if _, err := io.ReadFull(w.conn, ciphertext); err != nil {
		return err
	}

	nonce := counterNonce(w.readCounter, w.readAEAD.NonceSize())
	plaintext, err := w.readAEAD.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return err
	}
	w.readCounter++
	w.readBuffer = plaintext
	return nil
}

func (w *secureConn) Close() error {
	return w.conn.Close()
}

func (w *secureConn) LocalAddr() net.Addr {
	return w.conn.LocalAddr()
}

func (w *secureConn) RemoteAddr() net.Addr {
	return w.conn.RemoteAddr()
}

func (w *secureConn) SetDeadline(deadline time.Time) error {
	return w.conn.SetDeadline(deadline)
}

func (w *secureConn) SetReadDeadline(deadline time.Time) error {
	return w.conn.SetReadDeadline(deadline)
}

func (w *secureConn) SetWriteDeadline(deadline time.Time) error {
	return w.conn.SetWriteDeadline(deadline)
}

func counterNonce(counter uint64, nonceSize int) []byte {
	nonce := make([]byte, nonceSize)
	binary.BigEndian.PutUint64(nonce[len(nonce)-8:], counter)
	return nonce
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		count, err := writer.Write(data)
		if err != nil {
			return err
		}
		data = data[count:]
	}

	return nil
}

func writeJSONMessage(writer io.Writer, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(data)))
	copy(frame[4:], data)
	return writeAll(writer, frame)
}

func readJSONMessage(reader io.Reader, target any) error {
	lengthPrefix := make([]byte, 4)
	if _, err := io.ReadFull(reader, lengthPrefix); err != nil {
		return err
	}

	length := binary.BigEndian.Uint32(lengthPrefix)
	if length == 0 {
		return errors.New("secure transport handshake message is empty")
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(reader, data); err != nil {
		return err
	}

	return json.Unmarshal(data, target)
}

func decodeFixedBase64(encoded string, expectedLength int) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return nil, err
	}
	if len(decoded) != expectedLength {
		return nil, fmt.Errorf("invalid decoded key length: got %d, want %d", len(decoded), expectedLength)
	}

	return decoded, nil
}

func signProof(privateKey ed25519.PrivateKey, proof secureServerProof) (string, error) {
	data, err := json.Marshal(proof)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, data)), nil
}

func verifySignedProof(publicKey ed25519.PublicKey, proof secureServerProof, signature string) error {
	data, err := json.Marshal(proof)
	if err != nil {
		return err
	}

	decodedSignature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, data, decodedSignature) {
		return errors.New("bootstrap transport signature verification failed")
	}

	return nil
}

func recordRead(count int) {
	if count <= 0 {
		return
	}

	managerState.recordTransfer(transferEvent{
		recordedAt: currentTime(),
		readBytes:  uint64(count),
	})
}

func recordWrite(count int) {
	if count <= 0 {
		return
	}

	managerState.recordTransfer(transferEvent{
		recordedAt: currentTime(),
		writeBytes: uint64(count),
	})
}

func bytesPerSecondToMegabits(totalBytes uint64, window time.Duration) float64 {
	if totalBytes == 0 || window <= 0 {
		return 0
	}

	bitsPerSecond := (float64(totalBytes) * 8) / window.Seconds()
	return bitsPerSecond / bitsPerMegabit
}

func (s *trackedState) recordTransfer(event transferEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalReadBytes += event.readBytes
	s.totalWriteBytes += event.writeBytes
	s.transfers = append(s.transfers, event)
	s.trimTransfersLocked(event.recordedAt)
}

func (s *trackedState) snapshot(now time.Time) Usage {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.trimTransfersLocked(now)

	var readBytes uint64
	var writeBytes uint64
	for _, event := range s.transfers {
		readBytes += event.readBytes
		writeBytes += event.writeBytes
	}

	return Usage{
		ReadMbps:        bytesPerSecondToMegabits(readBytes, throughputWindow),
		WriteMbps:       bytesPerSecondToMegabits(writeBytes, throughputWindow),
		TotalReadBytes:  s.totalReadBytes,
		TotalWriteBytes: s.totalWriteBytes,
	}
}

func (s *trackedState) trimTransfersLocked(now time.Time) {
	cutoff := now.Add(-throughputWindow)
	keepFrom := 0
	for keepFrom < len(s.transfers) && s.transfers[keepFrom].recordedAt.Before(cutoff) {
		keepFrom++
	}

	if keepFrom == 0 {
		return
	}

	s.transfers = append([]transferEvent(nil), s.transfers[keepFrom:]...)
}
