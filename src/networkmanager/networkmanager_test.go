package networkmanager

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

const (
	readErrorFormat       = "Read() error = %v"
	writeErrorFormat      = "Write() error = %v"
	replyWantFormat       = "reply = %q, want %q"
	listenerErrorFormat   = "listener goroutine error = %v"
	generateKeyFormat     = "GenerateKey() error = %v"
	listenSecureFormat    = "ListenSecureTCP4() error = %v"
	totalReadBytesFormat  = "Snapshot().TotalReadBytes = %d, want %d"
	totalWriteBytesFormat = "Snapshot().TotalWriteBytes = %d, want %d"
	testLoopbackHost      = "127.0.0.1"
	testInvalidBase64     = "not-base64"
	testWriteFailedText   = "write failed"
	testKeyFailedText     = "key failed"
	testPublicKeyFailed   = "public key failed"
	testPayloadText       = "payload"
	testHelloText         = "hello"
)

func TestSnapshotReturnsZeroWhenNoTrafficRecorded(t *testing.T) {
	resetNetworkManagerTestState(t)

	got := Snapshot()
	if got.ReadMbps != 0 || got.WriteMbps != 0 || got.TotalReadBytes != 0 || got.TotalWriteBytes != 0 {
		t.Fatalf("Snapshot() = %#v, want zero usage", got)
	}
}

func TestWrapReadWriterTracksReadAndWriteThroughput(t *testing.T) {
	resetNetworkManagerTestState(t)

	now := time.Unix(1_700_000_000, 0)
	currentTime = func() time.Time {
		return now
	}

	target := &stubReadWriter{
		readData: []byte("peer"),
	}
	wrapped := WrapReadWriter(target)
	if wrapped == nil {
		t.Fatal("WrapReadWriter() = nil, want wrapper")
	}

	buffer := make([]byte, len(target.readData))
	readCount, err := wrapped.Read(buffer)
	if err != nil {
		t.Fatalf(readErrorFormat, err)
	}
	if readCount != len(target.readData) {
		t.Fatalf("Read() = %d, want %d", readCount, len(target.readData))
	}

	writeCount, err := wrapped.Write([]byte("uplink"))
	if err != nil {
		t.Fatalf(writeErrorFormat, err)
	}
	if writeCount != len("uplink") {
		t.Fatalf("Write() = %d, want %d", writeCount, len("uplink"))
	}

	usage := Snapshot()
	if usage.TotalReadBytes != uint64(len("peer")) {
		t.Fatalf(totalReadBytesFormat, usage.TotalReadBytes, len("peer"))
	}
	if usage.TotalWriteBytes != uint64(len("uplink")) {
		t.Fatalf(totalWriteBytesFormat, usage.TotalWriteBytes, len("uplink"))
	}
	if usage.ReadMbps <= 0 {
		t.Fatalf("Snapshot().ReadMbps = %f, want > 0", usage.ReadMbps)
	}
	if usage.WriteMbps <= 0 {
		t.Fatalf("Snapshot().WriteMbps = %f, want > 0", usage.WriteMbps)
	}
}

func TestWrapReadWriteCloserPreservesClose(t *testing.T) {
	resetNetworkManagerTestState(t)

	target := &stubReadWriteCloser{}
	wrapped := WrapReadWriteCloser(target)
	if wrapped == nil {
		t.Fatal("WrapReadWriteCloser() = nil, want wrapper")
	}

	if err := wrapped.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !target.closed {
		t.Fatal("Close() did not reach wrapped target")
	}
}

func TestWrapReadWriteCloserTracksReadAndWrite(t *testing.T) {
	resetNetworkManagerTestState(t)

	now := time.Unix(1_700_000_003, 0)
	currentTime = func() time.Time {
		return now
	}

	target := &stubReadWriteCloser{
		readData: []byte("seed"),
	}
	wrapped := WrapReadWriteCloser(target)
	if wrapped == nil {
		t.Fatal("WrapReadWriteCloser() = nil, want wrapper")
	}

	buffer := make([]byte, len(target.readData))
	readCount, err := wrapped.Read(buffer)
	if err != nil {
		t.Fatalf(readErrorFormat, err)
	}
	if readCount != len(target.readData) {
		t.Fatalf("Read() = %d, want %d", readCount, len(target.readData))
	}

	writeCount, err := wrapped.Write([]byte("uplink"))
	if err != nil {
		t.Fatalf(writeErrorFormat, err)
	}
	if writeCount != len("uplink") {
		t.Fatalf("Write() = %d, want %d", writeCount, len("uplink"))
	}

	usage := Snapshot()
	if usage.TotalReadBytes != uint64(len("seed")) {
		t.Fatalf(totalReadBytesFormat, usage.TotalReadBytes, len("seed"))
	}
	if usage.TotalWriteBytes != uint64(len("uplink")) {
		t.Fatalf(totalWriteBytesFormat, usage.TotalWriteBytes, len("uplink"))
	}
}

func TestWrapConnTracksTraffic(t *testing.T) {
	resetNetworkManagerTestState(t)

	now := time.Unix(1_700_000_001, 0)
	currentTime = func() time.Time {
		return now
	}

	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()

	done := make(chan error, 1)
	go func() {
		wrapped := WrapConn(left)
		if wrapped == nil {
			done <- io.ErrClosedPipe
			return
		}

		buffer := make([]byte, 4)
		if _, err := wrapped.Read(buffer); err != nil {
			done <- err
			return
		}
		if _, err := wrapped.Write([]byte("pong")); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	if _, err := right.Write([]byte("ping")); err != nil {
		t.Fatalf(writeErrorFormat, err)
	}

	reply := make([]byte, 4)
	if _, err := right.Read(reply); err != nil {
		t.Fatalf(readErrorFormat, err)
	}
	if !bytes.Equal(reply, []byte("pong")) {
		t.Fatalf(replyWantFormat, reply, "pong")
	}
	if err := <-done; err != nil {
		t.Fatalf("wrapped conn goroutine error = %v", err)
	}

	usage := Snapshot()
	if usage.TotalReadBytes != uint64(len("ping")) {
		t.Fatalf(totalReadBytesFormat, usage.TotalReadBytes, len("ping"))
	}
	if usage.TotalWriteBytes != uint64(len("pong")) {
		t.Fatalf(totalWriteBytesFormat, usage.TotalWriteBytes, len("pong"))
	}
}

func TestDialTCP4ReturnsTrackedConn(t *testing.T) {
	resetNetworkManagerTestState(t)

	now := time.Unix(1_700_000_004, 0)
	currentTime = func() time.Time {
		return now
	}

	listener, err := net.Listen("tcp4", net.JoinHostPort(testLoopbackHost, "0"))
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		serverConn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer serverConn.Close()

		buffer := make([]byte, 5)
		if _, err := io.ReadFull(serverConn, buffer); err != nil {
			done <- err
			return
		}
		if _, err := serverConn.Write([]byte("pong")); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	conn, err := DialTCP4(context.Background(), testLoopbackHost, port)
	if err != nil {
		t.Fatalf("DialTCP4() error = %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping!")); err != nil {
		t.Fatalf(writeErrorFormat, err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf(readErrorFormat, err)
	}
	if !bytes.Equal(reply, []byte("pong")) {
		t.Fatalf(replyWantFormat, reply, "pong")
	}
	if err := <-done; err != nil {
		t.Fatalf(listenerErrorFormat, err)
	}

	usage := Snapshot()
	if usage.TotalReadBytes != uint64(len("pong")) {
		t.Fatalf(totalReadBytesFormat, usage.TotalReadBytes, len("pong"))
	}
	if usage.TotalWriteBytes != uint64(len("ping!")) {
		t.Fatalf(totalWriteBytesFormat, usage.TotalWriteBytes, len("ping!"))
	}
}

func TestDialAndListenTCP4ReturnErrors(t *testing.T) {
	resetNetworkManagerTestState(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := DialTCP4(ctx, testLoopbackHost, 1); err == nil {
		t.Fatal("DialTCP4() error = nil, want canceled context failure")
	}
	if _, err := ListenTCP4(-1); err == nil {
		t.Fatal("ListenTCP4() error = nil, want invalid port failure")
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyFormat, err)
	}
	if _, err := DialSecureTCP4(ctx, testLoopbackHost, 1, ""); err == nil {
		t.Fatal("DialSecureTCP4() error = nil, want canceled context failure")
	}
	if _, err := ListenSecureTCP4(-1, privateKey); err == nil {
		t.Fatal("ListenSecureTCP4() error = nil, want invalid port failure")
	}
}

func TestListenTCP4TracksAcceptedConn(t *testing.T) {
	resetNetworkManagerTestState(t)

	now := time.Unix(1_700_000_005, 0)
	currentTime = func() time.Time {
		return now
	}

	listener, err := ListenTCP4(0)
	if err != nil {
		t.Fatalf("ListenTCP4() error = %v", err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		serverConn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer serverConn.Close()

		buffer := make([]byte, 4)
		if _, err := io.ReadFull(serverConn, buffer); err != nil {
			done <- err
			return
		}
		if _, err := serverConn.Write([]byte("pong!")); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	clientConn, err := net.Dial("tcp4", net.JoinHostPort(testLoopbackHost, fmt.Sprintf("%d", port)))
	if err != nil {
		t.Fatalf("net.Dial() error = %v", err)
	}
	defer clientConn.Close()

	if _, err := clientConn.Write([]byte("ping")); err != nil {
		t.Fatalf(writeErrorFormat, err)
	}
	reply := make([]byte, 5)
	if _, err := io.ReadFull(clientConn, reply); err != nil {
		t.Fatalf(readErrorFormat, err)
	}
	if !bytes.Equal(reply, []byte("pong!")) {
		t.Fatalf(replyWantFormat, reply, "pong!")
	}
	if err := <-done; err != nil {
		t.Fatalf(listenerErrorFormat, err)
	}

	usage := Snapshot()
	if usage.TotalReadBytes != uint64(len("ping")) {
		t.Fatalf(totalReadBytesFormat, usage.TotalReadBytes, len("ping"))
	}
	if usage.TotalWriteBytes != uint64(len("pong!")) {
		t.Fatalf(totalWriteBytesFormat, usage.TotalWriteBytes, len("pong!"))
	}
}

func TestTrackedListenersReturnAcceptErrors(t *testing.T) {
	resetNetworkManagerTestState(t)

	listener, err := net.Listen("tcp4", net.JoinHostPort(testLoopbackHost, "0"))
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("listener.Close() error = %v", err)
	}
	if _, err := (trackedListener{Listener: listener}).Accept(); err == nil {
		t.Fatal("trackedListener.Accept() error = nil, want closed listener failure")
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyFormat, err)
	}
	secureListener, err := net.Listen("tcp4", net.JoinHostPort(testLoopbackHost, "0"))
	if err != nil {
		t.Fatalf("net.Listen() secure error = %v", err)
	}
	if err := secureListener.Close(); err != nil {
		t.Fatalf("secureListener.Close() error = %v", err)
	}
	if _, err := (trackedSecureListener{Listener: secureListener, privateKey: privateKey}).Accept(); err == nil {
		t.Fatal("trackedSecureListener.Accept() error = nil, want closed listener failure")
	}
}

func TestDialSecureTCP4ReturnsTrackedConn(t *testing.T) {
	resetNetworkManagerTestState(t)

	now := time.Unix(1_700_000_006, 0)
	currentTime = func() time.Time {
		return now
	}

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyFormat, err)
	}
	expectedAccountID := accountIDFromPublicKey(publicKey)

	listener, err := ListenSecureTCP4(0, privateKey)
	if err != nil {
		t.Fatalf(listenSecureFormat, err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		serverConn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer serverConn.Close()

		buffer := make([]byte, 4)
		if _, err := io.ReadFull(serverConn, buffer); err != nil {
			done <- err
			return
		}
		if _, err := serverConn.Write([]byte("pong!")); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	conn, err := DialSecureTCP4(context.Background(), testLoopbackHost, port, expectedAccountID)
	if err != nil {
		t.Fatalf("DialSecureTCP4() error = %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf(writeErrorFormat, err)
	}
	reply := make([]byte, 5)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf(readErrorFormat, err)
	}
	if !bytes.Equal(reply, []byte("pong!")) {
		t.Fatalf(replyWantFormat, reply, "pong!")
	}
	if err := <-done; err != nil {
		t.Fatalf(listenerErrorFormat, err)
	}

	usage := Snapshot()
	if usage.TotalReadBytes < uint64(len("ping")+len("pong!")) {
		t.Fatalf("Snapshot().TotalReadBytes = %d, want at least %d", usage.TotalReadBytes, len("ping")+len("pong!"))
	}
	if usage.TotalWriteBytes < uint64(len("ping")+len("pong!")) {
		t.Fatalf("Snapshot().TotalWriteBytes = %d, want at least %d", usage.TotalWriteBytes, len("ping")+len("pong!"))
	}
}

func TestSecureConnDelegatesConnMethodsAndHandlesEmptyIO(t *testing.T) {
	resetNetworkManagerTestState(t)

	clientConn, serverConn := secureConnPair(t)
	defer clientConn.Close()
	defer serverConn.Close()

	if count, err := clientConn.Read(nil); err != nil || count != 0 {
		t.Fatalf("secureConn.Read(nil) = (%d, %v), want (0, nil)", count, err)
	}
	if count, err := clientConn.Write(nil); err != nil || count != 0 {
		t.Fatalf("secureConn.Write(nil) = (%d, %v), want (0, nil)", count, err)
	}

	deadline := time.Now().Add(time.Second)
	if err := clientConn.SetDeadline(deadline); err != nil {
		t.Fatalf("secureConn.SetDeadline() error = %v", err)
	}
	if err := clientConn.SetReadDeadline(deadline); err != nil {
		t.Fatalf("secureConn.SetReadDeadline() error = %v", err)
	}
	if err := clientConn.SetWriteDeadline(deadline); err != nil {
		t.Fatalf("secureConn.SetWriteDeadline() error = %v", err)
	}
	if clientConn.LocalAddr() == nil {
		t.Fatal("secureConn.LocalAddr() = nil, want address")
	}
	if clientConn.RemoteAddr() == nil {
		t.Fatal("secureConn.RemoteAddr() = nil, want address")
	}

	done := make(chan error, 1)
	go func() {
		_, err := serverConn.Write([]byte(testHelloText))
		done <- err
	}()

	first := make([]byte, 2)
	if _, err := io.ReadFull(clientConn, first); err != nil {
		t.Fatalf("secureConn.Read(first) error = %v", err)
	}
	second := make([]byte, 3)
	if _, err := io.ReadFull(clientConn, second); err != nil {
		t.Fatalf("secureConn.Read(second) error = %v", err)
	}
	if got := string(append(first, second...)); got != testHelloText {
		t.Fatalf("secureConn.Read() payload = %q, want %q", got, testHelloText)
	}
	if err := <-done; err != nil {
		t.Fatalf("secureConn.Write() goroutine error = %v", err)
	}
}

func TestSecureConnReturnsFrameErrors(t *testing.T) {
	resetNetworkManagerTestState(t)

	writeErr := errors.New(testWriteFailedText)
	secureWriter := mustNewSecureConn(t, &stubNetConn{writeErr: writeErr})
	if count, err := secureWriter.Write([]byte(testPayloadText)); !errors.Is(err, writeErr) || count != 0 {
		t.Fatalf("secureConn.Write() = (%d, %v), want write failure", count, err)
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "missing length", data: nil},
		{name: "empty frame", data: framedBytes(nil)},
		{name: "short ciphertext", data: appendFrameLength(nil, 4)},
		{name: "authentication failure", data: framedBytes([]byte("bad"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secureReader := mustNewSecureConn(t, &stubNetConn{reader: bytes.NewReader(tt.data)})
			if _, err := secureReader.Read(make([]byte, 1)); err == nil {
				t.Fatal("secureConn.Read() error = nil, want frame failure")
			}
		})
	}
}

func TestDialSecureTCP4RejectsUnexpectedAccountID(t *testing.T) {
	resetNetworkManagerTestState(t)

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyFormat, err)
	}
	otherPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() other error = %v", err)
	}

	listener, err := ListenSecureTCP4(0, privateKey)
	if err != nil {
		t.Fatalf(listenSecureFormat, err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		serverConn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer serverConn.Close()
		done <- nil
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	if _, err := DialSecureTCP4(context.Background(), testLoopbackHost, port, accountIDFromPublicKey(otherPublicKey)); err == nil {
		t.Fatal("DialSecureTCP4() error = nil, want account id verification failure")
	}
	if err := <-done; err != nil {
		t.Fatalf(listenerErrorFormat, err)
	}
}

func TestClientSecureConnReturnsHandshakeErrors(t *testing.T) {
	resetNetworkManagerTestState(t)

	writeErr := errors.New(testWriteFailedText)
	if _, err := clientSecureConn(context.Background(), &stubNetConn{writeErr: writeErr}, ""); !errors.Is(err, writeErr) {
		t.Fatalf("clientSecureConn() error = %v, want %v", err, writeErr)
	}
	keyErr := errors.New(testKeyFailedText)
	secureRandomReader = errReader{err: keyErr}
	if _, err := clientSecureConn(context.Background(), &stubNetConn{}, ""); !errors.Is(err, keyErr) {
		t.Fatalf("clientSecureConn() key error = %v, want %v", err, keyErr)
	}
	secureRandomReader = rand.Reader
	publicKeyErr := errors.New(testPublicKeyFailed)
	newX25519PublicKey = func([]byte) (*ecdh.PublicKey, error) {
		return nil, publicKeyErr
	}
	if err := runClientSecureConnWithResponse(t, "", func(t *testing.T, clientHello secureClientHello, privateKey ed25519.PrivateKey) secureServerHello {
		return validServerHello(t, clientHello, privateKey, mustX25519PublicKey(t))
	}); !errors.Is(err, publicKeyErr) {
		t.Fatalf("clientSecureConn() public key error = %v, want %v", err, publicKeyErr)
	}
	newX25519PublicKey = ecdh.X25519().NewPublicKey

	tests := []struct {
		name              string
		expectedAccountID string
		respond           func(t *testing.T, clientHello secureClientHello, privateKey ed25519.PrivateKey) secureServerHello
	}{
		{
			name: "protocol mismatch",
			respond: func(t *testing.T, clientHello secureClientHello, privateKey ed25519.PrivateKey) secureServerHello {
				hello := validServerHello(t, clientHello, privateKey, mustX25519PublicKey(t))
				hello.Protocol = "wrong-protocol"
				return hello
			},
		},
		{
			name: "invalid account pubkey",
			respond: func(t *testing.T, clientHello secureClientHello, privateKey ed25519.PrivateKey) secureServerHello {
				hello := validServerHello(t, clientHello, privateKey, mustX25519PublicKey(t))
				hello.AccountPubKey = testInvalidBase64
				return hello
			},
		},
		{
			name: "account id mismatch",
			respond: func(t *testing.T, clientHello secureClientHello, privateKey ed25519.PrivateKey) secureServerHello {
				hello := validServerHello(t, clientHello, privateKey, mustX25519PublicKey(t))
				hello.AccountID = "wrong-account"
				return hello
			},
		},
		{
			name:              "expected account id mismatch",
			expectedAccountID: "wrong-account",
			respond: func(t *testing.T, clientHello secureClientHello, privateKey ed25519.PrivateKey) secureServerHello {
				return validServerHello(t, clientHello, privateKey, mustX25519PublicKey(t))
			},
		},
		{
			name: "invalid server public key",
			respond: func(t *testing.T, clientHello secureClientHello, privateKey ed25519.PrivateKey) secureServerHello {
				hello := validServerHello(t, clientHello, privateKey, mustX25519PublicKey(t))
				hello.PublicKey = testInvalidBase64
				return hello
			},
		},
		{
			name: "invalid signature",
			respond: func(t *testing.T, clientHello secureClientHello, privateKey ed25519.PrivateKey) secureServerHello {
				hello := validServerHello(t, clientHello, privateKey, mustX25519PublicKey(t))
				hello.Signature = base64.StdEncoding.EncodeToString([]byte("bad-signature"))
				return hello
			},
		},
		{
			name: "low order server public key",
			respond: func(t *testing.T, clientHello secureClientHello, privateKey ed25519.PrivateKey) secureServerHello {
				return validServerHello(t, clientHello, privateKey, make([]byte, x25519KeyLength))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := runClientSecureConnWithResponse(t, tt.expectedAccountID, tt.respond); err == nil {
				t.Fatal("clientSecureConn() error = nil, want handshake failure")
			}
		})
	}

	t.Run("read server hello failure", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		done := make(chan error, 1)
		go func() {
			var clientHello secureClientHello
			if err := readJSONMessage(serverConn, &clientHello); err != nil {
				done <- err
				return
			}
			done <- serverConn.Close()
		}()

		if _, err := clientSecureConn(context.Background(), clientConn, ""); err == nil {
			t.Fatal("clientSecureConn() error = nil, want server hello read failure")
		}
		if err := <-done; err != nil {
			t.Fatalf("server goroutine error = %v", err)
		}
	})
}

func TestServerSecureConnReturnsHandshakeErrors(t *testing.T) {
	resetNetworkManagerTestState(t)

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyFormat, err)
	}

	if _, err := serverSecureConn(&stubNetConn{}, privateKey); err == nil {
		t.Fatal("serverSecureConn() error = nil, want client hello read failure")
	}
	publicKeyErr := errors.New(testPublicKeyFailed)
	newX25519PublicKey = func([]byte) (*ecdh.PublicKey, error) {
		return nil, publicKeyErr
	}
	if _, err := serverSecureConn(connWithClientHello(secureClientHello{Protocol: sessionProtocol, PublicKey: base64.StdEncoding.EncodeToString(mustX25519PublicKey(t))}, nil), privateKey); !errors.Is(err, publicKeyErr) {
		t.Fatalf("serverSecureConn() public key error = %v, want %v", err, publicKeyErr)
	}
	newX25519PublicKey = ecdh.X25519().NewPublicKey
	keyErr := errors.New(testKeyFailedText)
	secureRandomReader = errReader{err: keyErr}
	if _, err := serverSecureConn(connWithClientHello(secureClientHello{Protocol: sessionProtocol, PublicKey: base64.StdEncoding.EncodeToString(mustX25519PublicKey(t))}, nil), privateKey); !errors.Is(err, keyErr) {
		t.Fatalf("serverSecureConn() key error = %v, want %v", err, keyErr)
	}
	secureRandomReader = rand.Reader

	tests := []struct {
		name string
		conn net.Conn
	}{
		{
			name: "protocol mismatch",
			conn: connWithClientHello(secureClientHello{Protocol: "wrong-protocol", PublicKey: base64.StdEncoding.EncodeToString(mustX25519PublicKey(t))}, nil),
		},
		{
			name: "invalid client public key",
			conn: connWithClientHello(secureClientHello{Protocol: sessionProtocol, PublicKey: testInvalidBase64}, nil),
		},
		{
			name: "short client public key",
			conn: connWithClientHello(secureClientHello{Protocol: sessionProtocol, PublicKey: base64.StdEncoding.EncodeToString([]byte("short"))}, nil),
		},
		{
			name: "server hello write failure",
			conn: connWithClientHello(secureClientHello{Protocol: sessionProtocol, PublicKey: base64.StdEncoding.EncodeToString(mustX25519PublicKey(t))}, errors.New(testWriteFailedText)),
		},
		{
			name: "low order client public key",
			conn: connWithClientHello(secureClientHello{Protocol: sessionProtocol, PublicKey: base64.StdEncoding.EncodeToString(make([]byte, x25519KeyLength))}, nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := serverSecureConn(tt.conn, privateKey); err == nil {
				t.Fatal("serverSecureConn() error = nil, want handshake failure")
			}
		})
	}
}

func TestListenSecureTCP4IgnoresPlainTCPProbe(t *testing.T) {
	resetNetworkManagerTestState(t)

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyFormat, err)
	}

	listener, err := ListenSecureTCP4(0, privateKey)
	if err != nil {
		t.Fatalf(listenSecureFormat, err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		serverConn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer serverConn.Close()

		buffer := make([]byte, 4)
		if _, err := io.ReadFull(serverConn, buffer); err != nil {
			done <- err
			return
		}
		if _, err := serverConn.Write([]byte("pong")); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	probeConn, err := net.Dial("tcp4", net.JoinHostPort(testLoopbackHost, fmt.Sprintf("%d", port)))
	if err != nil {
		t.Fatalf("net.Dial() probe error = %v", err)
	}
	if err := probeConn.Close(); err != nil {
		t.Fatalf("probeConn.Close() error = %v", err)
	}

	conn, err := DialSecureTCP4(context.Background(), testLoopbackHost, port, accountIDFromPublicKey(publicKey))
	if err != nil {
		t.Fatalf("DialSecureTCP4() error = %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf(writeErrorFormat, err)
	}
	reply := make([]byte, 4)
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf(readErrorFormat, err)
	}
	if !bytes.Equal(reply, []byte("pong")) {
		t.Fatalf(replyWantFormat, reply, "pong")
	}
	if err := <-done; err != nil {
		t.Fatalf(listenerErrorFormat, err)
	}
}

func TestHandshakeHelpersReturnErrors(t *testing.T) {
	resetNetworkManagerTestState(t)

	if err := writeJSONMessage(io.Discard, make(chan int)); err == nil {
		t.Fatal("writeJSONMessage() error = nil, want marshal failure")
	}

	writeErr := errors.New(testWriteFailedText)
	if err := writeAll(&stubWriter{err: writeErr}, []byte(testPayloadText)); !errors.Is(err, writeErr) {
		t.Fatalf("writeAll() error = %v, want %v", err, writeErr)
	}
	chunked := &stubWriter{maxChunk: 2}
	if err := writeAll(chunked, []byte(testPayloadText)); err != nil {
		t.Fatalf("writeAll() partial writes error = %v", err)
	}
	if chunked.String() != testPayloadText {
		t.Fatalf("writeAll() partial data = %q, want %q", chunked.String(), testPayloadText)
	}

	if err := readJSONMessage(bytes.NewReader(framedBytes(nil)), &secureClientHello{}); err == nil {
		t.Fatal("readJSONMessage() error = nil, want empty message failure")
	}
	if err := readJSONMessage(bytes.NewReader(appendFrameLength(nil, 4)), &secureClientHello{}); err == nil {
		t.Fatal("readJSONMessage() error = nil, want short message failure")
	}
	if err := readJSONMessage(bytes.NewReader(framedBytes([]byte("not-json"))), &secureClientHello{}); err == nil {
		t.Fatal("readJSONMessage() error = nil, want unmarshal failure")
	}

	if _, err := decodeFixedBase64(testInvalidBase64, 1); err == nil {
		t.Fatal("decodeFixedBase64() error = nil, want base64 failure")
	}
	if _, err := decodeFixedBase64(base64.StdEncoding.EncodeToString([]byte("long")), 1); err == nil {
		t.Fatal("decodeFixedBase64() error = nil, want length failure")
	}

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyFormat, err)
	}
	proof := secureServerProof{Protocol: sessionProtocol}
	signature := signProof(privateKey, proof)
	if err := verifySignedProof(privateKey.Public().(ed25519.PublicKey), proof, testInvalidBase64); err == nil {
		t.Fatal("verifySignedProof() error = nil, want base64 failure")
	}
	if err := verifySignedProof(privateKey.Public().(ed25519.PublicKey), secureServerProof{Protocol: "changed"}, signature); err == nil {
		t.Fatal("verifySignedProof() error = nil, want signature failure")
	}
	if err := verifySignedProof(privateKey.Public().(ed25519.PublicKey), proof, signature); err != nil {
		t.Fatalf("verifySignedProof() error = %v", err)
	}
}

func TestListenSecureTCP4ReturnsInvalidKeyError(t *testing.T) {
	resetNetworkManagerTestState(t)

	if _, err := ListenSecureTCP4(0, ed25519.PrivateKey("short")); err == nil {
		t.Fatal("ListenSecureTCP4() error = nil, want invalid private key failure")
	}
}

func TestSnapshotExcludesOldTrafficFromCurrentWindow(t *testing.T) {
	resetNetworkManagerTestState(t)

	baseTime := time.Unix(1_700_000_002, 0)
	currentTime = func() time.Time {
		return baseTime
	}

	recordRead(len("old"))
	recordWrite(len("old"))

	currentTime = func() time.Time {
		return baseTime.Add(2 * time.Second)
	}

	usage := Snapshot()
	if usage.ReadMbps != 0 || usage.WriteMbps != 0 {
		t.Fatalf("Snapshot() current window = %#v, want zero throughput", usage)
	}
	if usage.TotalReadBytes != uint64(len("old")) || usage.TotalWriteBytes != uint64(len("old")) {
		t.Fatalf("Snapshot() lifetime totals = %#v, want retained totals", usage)
	}
}

func TestRecordReadAndWriteIgnoreZeroOrNegativeCounts(t *testing.T) {
	resetNetworkManagerTestState(t)

	recordRead(0)
	recordRead(-1)
	recordWrite(0)
	recordWrite(-1)

	usage := Snapshot()
	if usage.ReadMbps != 0 || usage.WriteMbps != 0 || usage.TotalReadBytes != 0 || usage.TotalWriteBytes != 0 {
		t.Fatalf("Snapshot() = %#v, want zero usage", usage)
	}
	if len(managerState.transfers) != 0 {
		t.Fatalf("len(managerState.transfers) = %d, want %d", len(managerState.transfers), 0)
	}
}

func TestWrapHelpersReturnNilForNilTargets(t *testing.T) {
	resetNetworkManagerTestState(t)

	if WrapReadWriter(nil) != nil {
		t.Fatal("WrapReadWriter(nil) = non-nil, want nil")
	}
	if WrapReadWriteCloser(nil) != nil {
		t.Fatal("WrapReadWriteCloser(nil) = non-nil, want nil")
	}
	if WrapConn(nil) != nil {
		t.Fatal("WrapConn(nil) = non-nil, want nil")
	}
}

func resetNetworkManagerTestState(t *testing.T) {
	originalCurrentTime := currentTime
	originalSecureRandomReader := secureRandomReader
	originalNewX25519PublicKey := newX25519PublicKey
	currentTime = time.Now
	secureRandomReader = rand.Reader
	newX25519PublicKey = ecdh.X25519().NewPublicKey
	managerState = trackedState{}

	t.Cleanup(func() {
		currentTime = originalCurrentTime
		secureRandomReader = originalSecureRandomReader
		newX25519PublicKey = originalNewX25519PublicKey
		managerState = trackedState{}
	})
}

type stubReadWriter struct {
	readData []byte
}

func (s *stubReadWriter) Read(buffer []byte) (int, error) {
	copy(buffer, s.readData)
	return len(s.readData), nil
}

func (s *stubReadWriter) Write(buffer []byte) (int, error) {
	return len(buffer), nil
}

type stubReadWriteCloser struct {
	readData []byte
	closed   bool
}

func (s *stubReadWriteCloser) Read(buffer []byte) (int, error) {
	if len(s.readData) == 0 {
		return 0, io.EOF
	}

	copy(buffer, s.readData)
	return len(s.readData), nil
}

func (s *stubReadWriteCloser) Write(buffer []byte) (int, error) {
	return len(buffer), nil
}

func (s *stubReadWriteCloser) Close() error {
	s.closed = true
	return nil
}

type stubNetConn struct {
	reader      io.Reader
	writeBuffer bytes.Buffer
	writeErr    error
}

func (s *stubNetConn) Read(buffer []byte) (int, error) {
	if s.reader == nil {
		return 0, io.EOF
	}

	return s.reader.Read(buffer)
}

func (s *stubNetConn) Write(buffer []byte) (int, error) {
	if s.writeErr != nil {
		return 0, s.writeErr
	}

	return s.writeBuffer.Write(buffer)
}

func (s *stubNetConn) Close() error {
	return nil
}

func (s *stubNetConn) LocalAddr() net.Addr {
	return stubAddr("local")
}

func (s *stubNetConn) RemoteAddr() net.Addr {
	return stubAddr("remote")
}

func (s *stubNetConn) SetDeadline(time.Time) error {
	return nil
}

func (s *stubNetConn) SetReadDeadline(time.Time) error {
	return nil
}

func (s *stubNetConn) SetWriteDeadline(time.Time) error {
	return nil
}

type stubAddr string

func (s stubAddr) Network() string {
	return string(s)
}

func (s stubAddr) String() string {
	return string(s)
}

type stubWriter struct {
	bytes.Buffer
	maxChunk int
	err      error
}

func (s *stubWriter) Write(data []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	if s.maxChunk > 0 && len(data) > s.maxChunk {
		data = data[:s.maxChunk]
	}

	return s.Buffer.Write(data)
}

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
}

func secureConnPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyFormat, err)
	}

	clientRaw, serverRaw := net.Pipe()
	serverReady := make(chan struct {
		conn net.Conn
		err  error
	}, 1)
	go func() {
		conn, err := serverSecureConn(serverRaw, privateKey)
		serverReady <- struct {
			conn net.Conn
			err  error
		}{conn: conn, err: err}
	}()

	clientConn, err := clientSecureConn(context.Background(), clientRaw, accountIDFromPublicKey(publicKey))
	if err != nil {
		t.Fatalf("clientSecureConn() error = %v", err)
	}
	serverResult := <-serverReady
	if serverResult.err != nil {
		t.Fatalf("serverSecureConn() error = %v", serverResult.err)
	}

	return clientConn, serverResult.conn
}

func mustNewSecureConn(t *testing.T, conn net.Conn) *secureConn {
	t.Helper()

	secure := newSecureConn(conn, []byte("shared-secret"), bytes.Repeat([]byte{1}, x25519KeyLength), bytes.Repeat([]byte{2}, x25519KeyLength), true)
	return secure.(*secureConn)
}

func mustX25519PublicKey(t *testing.T) []byte {
	t.Helper()

	privateKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey(X25519) error = %v", err)
	}

	return privateKey.PublicKey().Bytes()
}

func validServerHello(t *testing.T, clientHello secureClientHello, privateKey ed25519.PrivateKey, serverPublicKey []byte) secureServerHello {
	t.Helper()

	accountID := accountIDFromPublicKey(privateKey.Public().(ed25519.PublicKey))
	proof := secureServerProof{
		Protocol:        sessionProtocol,
		ClientPublicKey: clientHello.PublicKey,
		ServerPublicKey: base64.StdEncoding.EncodeToString(serverPublicKey),
		AccountID:       accountID,
	}
	signature := signProof(privateKey, proof)

	return secureServerHello{
		Protocol:      sessionProtocol,
		AccountID:     accountID,
		AccountPubKey: base64.StdEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		PublicKey:     proof.ServerPublicKey,
		Signature:     signature,
	}
}

func runClientSecureConnWithResponse(t *testing.T, expectedAccountID string, respond func(*testing.T, secureClientHello, ed25519.PrivateKey) secureServerHello) error {
	t.Helper()

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(generateKeyFormat, err)
	}

	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	done := make(chan error, 1)
	go func() {
		defer serverConn.Close()

		var clientHello secureClientHello
		if err := readJSONMessage(serverConn, &clientHello); err != nil {
			done <- err
			return
		}
		done <- writeJSONMessage(serverConn, respond(t, clientHello, privateKey))
	}()

	_, err = clientSecureConn(context.Background(), clientConn, expectedAccountID)
	if serverErr := <-done; serverErr != nil {
		t.Fatalf("server response goroutine error = %v", serverErr)
	}

	return err
}

func connWithClientHello(hello secureClientHello, writeErr error) net.Conn {
	return &stubNetConn{
		reader:   bytes.NewReader(framedJSON(hello)),
		writeErr: writeErr,
	}
}

func framedJSON(payload any) []byte {
	data, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}

	return framedBytes(data)
}

func framedBytes(data []byte) []byte {
	return appendFrameLength(data, uint32(len(data)))
}

func appendFrameLength(data []byte, length uint32) []byte {
	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame[:4], length)
	copy(frame[4:], data)
	return frame
}
