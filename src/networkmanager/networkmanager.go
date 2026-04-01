package networkmanager

import (
	"io"
	"net"
	"sync"
	"time"
)

const (
	throughputWindow = time.Second
	bitsPerMegabit   = 1_000_000
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

// Snapshot returns the current one-second throughput window plus lifetime totals.
func Snapshot() Usage {
	return managerState.snapshot(currentTime())
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
