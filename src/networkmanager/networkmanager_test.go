package networkmanager

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

const (
	readErrorFormat       = "Read() error = %v"
	writeErrorFormat      = "Write() error = %v"
	totalReadBytesFormat  = "Snapshot().TotalReadBytes = %d, want %d"
	totalWriteBytesFormat = "Snapshot().TotalWriteBytes = %d, want %d"
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
		t.Fatalf("reply = %q, want %q", reply, "pong")
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

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
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
	conn, err := DialTCP4(context.Background(), "127.0.0.1", port)
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
		t.Fatalf("reply = %q, want %q", reply, "pong")
	}
	if err := <-done; err != nil {
		t.Fatalf("listener goroutine error = %v", err)
	}

	usage := Snapshot()
	if usage.TotalReadBytes != uint64(len("pong")) {
		t.Fatalf(totalReadBytesFormat, usage.TotalReadBytes, len("pong"))
	}
	if usage.TotalWriteBytes != uint64(len("ping!")) {
		t.Fatalf(totalWriteBytesFormat, usage.TotalWriteBytes, len("ping!"))
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
	clientConn, err := net.Dial("tcp4", net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)))
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
		t.Fatalf("reply = %q, want %q", reply, "pong!")
	}
	if err := <-done; err != nil {
		t.Fatalf("listener goroutine error = %v", err)
	}

	usage := Snapshot()
	if usage.TotalReadBytes != uint64(len("ping")) {
		t.Fatalf(totalReadBytesFormat, usage.TotalReadBytes, len("ping"))
	}
	if usage.TotalWriteBytes != uint64(len("pong!")) {
		t.Fatalf(totalWriteBytesFormat, usage.TotalWriteBytes, len("pong!"))
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
		t.Fatalf("GenerateKey() error = %v", err)
	}
	expectedAccountID := accountIDFromPublicKey(publicKey)

	listener, err := ListenSecureTCP4(0, privateKey)
	if err != nil {
		t.Fatalf("ListenSecureTCP4() error = %v", err)
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

		tlsConn, ok := serverConn.(*tls.Conn)
		if !ok {
			done <- fmt.Errorf("Accept() = %T, want *tls.Conn", serverConn)
			return
		}
		if err := tlsConn.Handshake(); err != nil {
			done <- err
			return
		}

		buffer := make([]byte, 4)
		if _, err := io.ReadFull(tlsConn, buffer); err != nil {
			done <- err
			return
		}
		if _, err := tlsConn.Write([]byte("pong!")); err != nil {
			done <- err
			return
		}
		done <- nil
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	conn, err := DialSecureTCP4(context.Background(), "127.0.0.1", port, expectedAccountID)
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
		t.Fatalf("reply = %q, want %q", reply, "pong!")
	}
	if err := <-done; err != nil {
		t.Fatalf("listener goroutine error = %v", err)
	}

	usage := Snapshot()
	if usage.TotalReadBytes < uint64(len("ping")+len("pong!")) {
		t.Fatalf("Snapshot().TotalReadBytes = %d, want at least %d", usage.TotalReadBytes, len("ping")+len("pong!"))
	}
	if usage.TotalWriteBytes < uint64(len("ping")+len("pong!")) {
		t.Fatalf("Snapshot().TotalWriteBytes = %d, want at least %d", usage.TotalWriteBytes, len("ping")+len("pong!"))
	}
}

func TestDialSecureTCP4RejectsUnexpectedAccountID(t *testing.T) {
	resetNetworkManagerTestState(t)

	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	otherPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() other error = %v", err)
	}

	listener, err := ListenSecureTCP4(0, privateKey)
	if err != nil {
		t.Fatalf("ListenSecureTCP4() error = %v", err)
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

		tlsConn, ok := serverConn.(*tls.Conn)
		if !ok {
			done <- fmt.Errorf("Accept() = %T, want *tls.Conn", serverConn)
			return
		}
		done <- tlsConn.Handshake()
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	if _, err := DialSecureTCP4(context.Background(), "127.0.0.1", port, accountIDFromPublicKey(otherPublicKey)); err == nil {
		t.Fatal("DialSecureTCP4() error = nil, want account id verification failure")
	}
	if err := <-done; err != nil && !strings.Contains(err.Error(), "bad certificate") {
		t.Fatalf("listener goroutine error = %v", err)
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
	currentTime = time.Now
	managerState = trackedState{}

	t.Cleanup(func() {
		currentTime = originalCurrentTime
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
