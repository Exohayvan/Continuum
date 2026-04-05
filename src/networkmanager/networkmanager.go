package networkmanager

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	throughputWindow = time.Second
	bitsPerMegabit   = 1_000_000
	sessionProtocol  = "continuum-bootstrap/1"
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
	tlsConfig *tls.Config
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

// DialSecureTCP4 opens an encrypted tcp4 connection. When expectedAccountID is
// provided, the peer certificate's ed25519 public key must hash to that account id.
func DialSecureTCP4(ctx context.Context, host string, port int, expectedAccountID string) (net.Conn, error) {
	rawConn, err := dialTCP4(ctx, host, port)
	if err != nil {
		return nil, err
	}

	trackedConn := WrapConn(rawConn)
	tlsConn := tls.Client(trackedConn, clientTLSConfig(expectedAccountID))
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = tlsConn.Close()
		return nil, err
	}

	return tlsConn, nil
}

// ListenSecureTCP4 opens an encrypted tcp4 listener using the supplied ed25519
// identity key for the server certificate.
func ListenSecureTCP4(port int, privateKey ed25519.PrivateKey) (net.Listener, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, errors.New("bootstrap transport private key is invalid")
	}

	rawListener, err := listenTCP4(port)
	if err != nil {
		return nil, err
	}

	certificate, err := sessionCertificate(privateKey)
	if err != nil {
		_ = rawListener.Close()
		return nil, err
	}

	return trackedSecureListener{
		Listener: rawListener,
		tlsConfig: &tls.Config{
			MinVersion:   tls.VersionTLS13,
			Certificates: []tls.Certificate{certificate},
			NextProtos:   []string{sessionProtocol},
		},
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
	conn, err := w.Listener.Accept()
	if err != nil {
		return nil, err
	}

	return tls.Server(WrapConn(conn), w.tlsConfig), nil
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

func clientTLSConfig(expectedAccountID string) *tls.Config {
	expectedAccountID = strings.TrimSpace(expectedAccountID)
	config := &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true,
		NextProtos:         []string{sessionProtocol},
	}
	if expectedAccountID == "" {
		return config
	}

	config.VerifyConnection = func(state tls.ConnectionState) error {
		return verifyPeerAccountID(state, expectedAccountID)
	}

	return config
}

func verifyPeerAccountID(state tls.ConnectionState, expectedAccountID string) error {
	if len(state.PeerCertificates) == 0 {
		return errors.New("bootstrap transport did not present a certificate")
	}

	publicKey, ok := state.PeerCertificates[0].PublicKey.(ed25519.PublicKey)
	if !ok {
		return errors.New("bootstrap transport certificate does not use an ed25519 public key")
	}
	if accountIDFromPublicKey(publicKey) != expectedAccountID {
		return errors.New("bootstrap transport account id does not match expected bootstrap account")
	}

	return nil
}

func sessionCertificate(privateKey ed25519.PrivateKey) (tls.Certificate, error) {
	now := currentTime().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()),
		Subject: pkix.Name{
			CommonName: "Continuum Bootstrap Transport",
		},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, privateKey.Public(), privateKey)
	if err != nil {
		return tls.Certificate{}, err
	}

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}

	return tls.Certificate{
		Certificate: [][]byte{der},
		PrivateKey:  privateKey,
		Leaf:        leaf,
	}, nil
}

func accountIDFromPublicKey(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return hex.EncodeToString(sum[:])
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
