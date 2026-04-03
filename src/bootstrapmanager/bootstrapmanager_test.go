package bootstrapmanager

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"continuum/src/accounts"
	"continuum/src/datamanager"
	"continuum/src/nodeid"
)

const (
	testBootstrapHost      = "162.191.52.239"
	testBootstrapTimestamp = "2026-04-02T00:00:00Z"
	testJoiningNodeID      = "joining-node"
	testJoiningPeerFile    = "joining-node.peer"
	testJoiningMetaFile    = "joining-node.meta"
	testDecodeErrorFormat  = "Decode() error = %v"
	testEncodeErrorFormat  = "Encode() error = %v"
	testAccountID          = "account-123"
	testAccountPassword    = "secret-pass"
	testAccountKeyFile     = "account-123.key"
	testAccountBlobFile    = "account-123.blob"
	testAccountBlobJSON    = `{"AccountID":"account-123"}`
	testSessionID          = "session-123"
	testRecoveryPassword   = "recovery-pass"
	testRecoverySessionID  = "session-recover"
	testBootstrapNodeID    = "264648e40c71d6385d470ca4c8e5156a1abb74af6aa1e92a948066139a5b5e45"
)

func TestLoadStateReturnsExistingPeerCount(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	dataPath := filepath.Join(t.TempDir(), "data")
	peersPath := filepath.Join(dataPath, "network", "peers")
	if err := os.MkdirAll(peersPath, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(peersPath, "known-node.peer"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(peersPath, "notes.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	ensureDataLayout = func() (string, error) {
		return dataPath, nil
	}
	fetchCalled := false
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		fetchCalled = true
		return nil, nil
	}

	state := LoadState()
	if state.NeedsBootstrap {
		t.Fatal("LoadState() NeedsBootstrap = true, want false")
	}
	if state.PeerCount != 1 {
		t.Fatalf("LoadState() PeerCount = %d, want %d", state.PeerCount, 1)
	}
	if fetchCalled {
		t.Fatal("LoadState() fetched bootstrap list despite existing peer file")
	}
}

func TestLoadStateFetchesAndSortsBootstrapNodes(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	dataPath := filepath.Join(t.TempDir(), "data")
	peersPath := filepath.Join(dataPath, "network", "peers")
	if err := os.MkdirAll(peersPath, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	ensureDataLayout = func() (string, error) {
		return dataPath, nil
	}
	resolveNodeID = func() string { return "" }
	bootstrapList := loadBootstrapListFixture(t)
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return bootstrapList, nil
	}
	probeEndpoint = func(host string, port int) (time.Duration, error) {
		if host != testBootstrapHost {
			t.Fatalf("probeEndpoint() host = %q, want %q", host, testBootstrapHost)
		}
		return 12 * time.Millisecond, nil
	}

	state := LoadState()
	if !state.NeedsBootstrap {
		t.Fatal("LoadState() NeedsBootstrap = false, want true")
	}
	if len(state.Nodes) != 1 {
		t.Fatalf("len(LoadState().Nodes) = %d, want %d", len(state.Nodes), 1)
	}
	if state.Nodes[0].Name != "na-east" || state.Nodes[0].LatencyMilliseconds != 12 {
		t.Fatalf("LoadState().Nodes[0] = %#v, want na-east first with 12ms", state.Nodes[0])
	}
}

func TestBuildBootstrapStartResponseUsesObservedIPv4AndRecovery(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, privateKey, blobData, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf("buildAccountFixtures() error = %v", err)
	}

	probeEndpoint = func(string, int) (time.Duration, error) {
		return 5 * time.Millisecond, nil
	}
	readManagedFile = func(path string) ([]byte, error) {
		switch path {
		case testPeerPath():
			return []byte(`{}`), nil
		case testMetaPath():
			return buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 7), nil
		case filepath.Join("network", "accounts", accountID+pubkeyFileSuffix):
			return accountPubKeyData, nil
		case filepath.Join("network", "accounts", accountID+metaFileSuffix):
			return accountMetaData, nil
		case filepath.Join("network", "accounts", accountID+accountBlobFileSuffix):
			return blobData, nil
		default:
			return nil, os.ErrNotExist
		}
	}

	response := buildBootstrapStartResponse(&net.TCPAddr{IP: net.ParseIP(testBootstrapHost), Port: 43001}, bootstrapSessionStartRequest{
		Type:       "start",
		NodeID:     testJoiningNodeID,
		ListenPort: 58103,
	})

	if response.ObservedIPv4 != testBootstrapHost {
		t.Fatalf("buildBootstrapStartResponse().ObservedIPv4 = %q, want %q", response.ObservedIPv4, testBootstrapHost)
	}
	if response.Port != 58103 {
		t.Fatalf("buildBootstrapStartResponse().Port = %d, want %d", response.Port, 58103)
	}
	if !response.Reachable {
		t.Fatal("buildBootstrapStartResponse().Reachable = false, want true")
	}
	if !response.RecoveryAvailable {
		t.Fatal("buildBootstrapStartResponse().RecoveryAvailable = false, want true")
	}
	if response.AccountID != accountID {
		t.Fatalf("buildBootstrapStartResponse().AccountID = %q, want %q", response.AccountID, accountID)
	}
	if response.FirstSeen != testBootstrapTimestamp {
		t.Fatalf("buildBootstrapStartResponse().FirstSeen = %q, want %q", response.FirstSeen, testBootstrapTimestamp)
	}
	if response.Revision != 8 {
		t.Fatalf("buildBootstrapStartResponse().Revision = %d, want %d", response.Revision, 8)
	}
	if response.AccountCreatedAt != testBootstrapTimestamp {
		t.Fatalf("buildBootstrapStartResponse().AccountCreatedAt = %q, want %q", response.AccountCreatedAt, testBootstrapTimestamp)
	}
	if response.AccountRevision != 2 {
		t.Fatalf("buildBootstrapStartResponse().AccountRevision = %d, want %d", response.AccountRevision, 2)
	}
}

func TestConnectReturnsAwaitingPasswordSession(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string {
		return testJoiningNodeID
	}
	bootstrapList := loadBootstrapListFixture(t)
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return bootstrapList, nil
	}

	serverConn, clientConn := net.Pipe()
	dialBootstrap = func(context.Context, string, int) (net.Conn, error) {
		return clientConn, nil
	}

	go func() {
		defer serverConn.Close()

		var request bootstrapSessionStartRequest
		if err := json.NewDecoder(serverConn).Decode(&request); err != nil {
			t.Errorf(testDecodeErrorFormat, err)
			return
		}
		if request.NodeID != testJoiningNodeID {
			t.Errorf("request.NodeID = %q, want %q", request.NodeID, testJoiningNodeID)
		}

		if err := json.NewEncoder(serverConn).Encode(bootstrapSessionStartResponse{
			ObservedIPv4:      testBootstrapHost,
			Port:              58103,
			Reachable:         true,
			RecoveryAvailable: true,
			AccountID:         testAccountID,
			FirstSeen:         testBootstrapTimestamp,
			Revision:          4,
		}); err != nil {
			t.Errorf(testEncodeErrorFormat, err)
		}
	}()

	result, err := Connect(testBootstrapHost, 58103, "")
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if result.Connected {
		t.Fatal("Connect() Connected = true, want false")
	}
	if !result.AwaitingPassword {
		t.Fatal("Connect() AwaitingPassword = false, want true")
	}
	if result.SessionID == "" {
		t.Fatal("Connect() SessionID = empty, want non-empty")
	}
	if result.AccountID != testAccountID {
		t.Fatalf("Connect() AccountID = %q, want %q", result.AccountID, testAccountID)
	}

	removePendingSession(result.SessionID)
}

func TestFetchRemoteBootstrapListReturnsResponseBody(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	bootstrapList := loadBootstrapListFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet {
			t.Fatalf("request.Method = %q, want %q", request.Method, http.MethodGet)
		}
		if request.Header.Get("User-Agent") != "continuum-bootstrap" {
			t.Fatalf("User-Agent = %q, want %q", request.Header.Get("User-Agent"), "continuum-bootstrap")
		}
		_, _ = responseWriter.Write(bootstrapList)
	}))
	defer server.Close()

	httpClient = server.Client()

	data, err := fetchRemoteBootstrapList(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("fetchRemoteBootstrapList() error = %v", err)
	}
	if string(data) != string(bootstrapList) {
		t.Fatalf("fetchRemoteBootstrapList() = %q, want %q", string(data), string(bootstrapList))
	}
}

func TestMeasureEndpointLatency(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()

	accepted := make(chan struct{})
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			close(accepted)
			_ = conn.Close()
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	latency, err := measureEndpointLatency("127.0.0.1", port)
	if err != nil {
		t.Fatalf("measureEndpointLatency() error = %v", err)
	}
	if latency <= 0 {
		t.Fatalf("measureEndpointLatency() = %s, want > 0", latency)
	}
	<-accepted
}

func TestDialAndListenBootstrapEndpoints(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	listener, err := listenBootstrapEndpoint(0)
	if err != nil {
		t.Fatalf("listenBootstrapEndpoint() error = %v", err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		_ = conn.Close()
		done <- nil
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	conn, err := dialBootstrapEndpoint(context.Background(), "127.0.0.1", port)
	if err != nil {
		t.Fatalf("dialBootstrapEndpoint() error = %v", err)
	}
	_ = conn.Close()

	if err := <-done; err != nil {
		t.Fatalf("listener.Accept() error = %v", err)
	}
}

func TestStartServiceInvokesBootstrapLoop(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testBootstrapNodeID }
	bootstrapList := loadBootstrapListFixture(t)
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return bootstrapList, nil
	}

	called := make(chan struct{}, 1)
	listenBootstrap = func(int) (net.Listener, error) {
		called <- struct{}{}
		return &failingListener{acceptErr: errors.New("stop")}, nil
	}

	StartService()

	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("StartService() did not attempt to start the bootstrap listener")
	}
}

func TestStartBootstrapServiceReturnsAcceptError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testBootstrapNodeID }
	bootstrapList := loadBootstrapListFixture(t)
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return bootstrapList, nil
	}
	wantErr := errors.New("listener stopped")
	listenBootstrap = func(int) (net.Listener, error) {
		return &failingListener{acceptErr: wantErr}, nil
	}

	err := startBootstrapService(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("startBootstrapService() error = %v, want %v", err, wantErr)
	}
}

func TestHandleBootstrapConnectionAcceptsValidFinalizeRequest(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	baseTime := time.Now().Add(time.Minute)
	currentTime = func() time.Time {
		return baseTime
	}
	readManagedFile = func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	probeEndpoint = func(string, int) (time.Duration, error) {
		return 5 * time.Millisecond, nil
	}

	accountID, privateKey, blobData, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf("buildAccountFixtures() error = %v", err)
	}

	writtenFiles := map[string][]byte{}
	writeManagedFile = func(path string, data []byte, perm os.FileMode) error {
		writtenFiles[path] = append([]byte(nil), data...)
		return nil
	}

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	go handleBootstrapConnection(connWithRemoteAddr{
		Conn:       serverConn,
		remoteAddr: &net.TCPAddr{IP: net.ParseIP(testBootstrapHost), Port: 43001},
	})

	if err := json.NewEncoder(clientConn).Encode(bootstrapSessionStartRequest{
		Type:       "start",
		NodeID:     testJoiningNodeID,
		ListenPort: 58103,
	}); err != nil {
		t.Fatalf("Encode(start request) error = %v", err)
	}

	var startResponse bootstrapSessionStartResponse
	if err := json.NewDecoder(clientConn).Decode(&startResponse); err != nil {
		t.Fatalf("Decode(start response) error = %v", err)
	}
	if startResponse.Error != "" {
		t.Fatalf("startResponse.Error = %q, want empty", startResponse.Error)
	}

	peerData := buildSignedPeerFixture(t, privateKey, startResponse.ObservedIPv4, startResponse.Port, accountID)
	metaData := buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, startResponse.FirstSeen, startResponse.Revision)
	if err := json.NewEncoder(clientConn).Encode(bootstrapSessionFinalizeRequest{
		Type:          "finalize",
		NodeID:        testJoiningNodeID,
		AccountID:     accountID,
		PeerData:      peerData,
		MetaData:      metaData,
		AccountBlob:   blobData,
		AccountPubKey: accountPubKeyData,
		AccountMeta:   accountMetaData,
	}); err != nil {
		t.Fatalf("Encode(finalize request) error = %v", err)
	}

	var finalizeResponse bootstrapSessionFinalizeResponse
	if err := json.NewDecoder(clientConn).Decode(&finalizeResponse); err != nil {
		t.Fatalf("Decode(finalize response) error = %v", err)
	}
	if finalizeResponse.Error != "" {
		t.Fatalf("finalizeResponse.Error = %q, want empty", finalizeResponse.Error)
	}
	if _, ok := writtenFiles[filepath.Join("network", "accounts", accountID+pubkeyFileSuffix)]; !ok {
		t.Fatal("handleBootstrapConnection() did not write the account pubkey")
	}
}

func TestCompleteCreatesFilesAndFinalizesSession(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	createAccount = func(password string) (accounts.Material, error) {
		if password != testAccountPassword {
			t.Fatalf("createAccount() password = %q, want %q", password, testAccountPassword)
		}
		return material, nil
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	session := &pendingSession{
		id:     testSessionID,
		conn:   clientConn,
		nodeID: testJoiningNodeID,
		response: bootstrapSessionStartResponse{
			ObservedIPv4: testBootstrapHost,
			Port:         58103,
			Reachable:    true,
			FirstSeen:    testBootstrapTimestamp,
			Revision:     1,
		},
	}
	storePendingSession(session)
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})

	writtenFiles := map[string][]byte{}
	writeManagedFile = func(path string, data []byte, perm os.FileMode) error {
		writtenFiles[path] = append([]byte(nil), data...)
		return nil
	}

	go respondToFinalizeRequest(t, serverConn)

	result, err := Complete(testSessionID, testAccountPassword)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	assertCompleteResult(t, result)
	assertBootstrapFilesWritten(t, writtenFiles)
}

func TestCompleteRecoversExistingAccountBlob(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	recoverCalled := false
	recoverAccount = func(blobData []byte, password string) (accounts.Material, error) {
		recoverCalled = true
		if string(blobData) != `{"blob":"data"}` {
			t.Fatalf("recoverAccount() blobData = %s, want %s", string(blobData), `{"blob":"data"}`)
		}
		if password != testRecoveryPassword {
			t.Fatalf("recoverAccount() password = %q, want %q", password, testRecoveryPassword)
		}
		return accounts.Material{
			AccountID:    testAccountID,
			LocalKeyPath: testAccountKeyPath(),
			PublicKey:    publicKey,
			PrivateKey:   privateKey,
			BlobData:     []byte(`{"blob":"data"}`),
		}, nil
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	session := &pendingSession{
		id:     testRecoverySessionID,
		conn:   clientConn,
		nodeID: testJoiningNodeID,
		response: bootstrapSessionStartResponse{
			ObservedIPv4:      testBootstrapHost,
			Port:              58103,
			Reachable:         false,
			RecoveryAvailable: true,
			AccountID:         testAccountID,
			AccountBlob:       []byte(`{"blob":"data"}`),
			FirstSeen:         testBootstrapTimestamp,
			Revision:          9,
		},
	}
	storePendingSession(session)
	t.Cleanup(func() {
		removePendingSession(testRecoverySessionID)
	})

	writeManagedFile = func(string, []byte, os.FileMode) error { return nil }
	go func() {
		var finalizeRequest bootstrapSessionFinalizeRequest
		if err := json.NewDecoder(serverConn).Decode(&finalizeRequest); err != nil {
			t.Errorf(testDecodeErrorFormat, err)
			return
		}
		if err := json.NewEncoder(serverConn).Encode(bootstrapSessionFinalizeResponse{}); err != nil {
			t.Errorf(testEncodeErrorFormat, err)
		}
	}()

	result, err := Complete(testRecoverySessionID, testRecoveryPassword)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if !recoverCalled {
		t.Fatal("Complete() did not use recoverAccount() for a recovery session")
	}
	if result.Reachable {
		t.Fatal("Complete() Reachable = true, want false")
	}
}

func TestValidateFinalizeRequestRejectsNodeAccountTakeover(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	originalAccountID, originalPrivateKey, originalBlobData, originalPubKeyData, originalAccountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf("buildAccountFixtures() original error = %v", err)
	}
	newAccountID, newPrivateKey, newBlobData, newPubKeyData, newAccountMetaData, err := buildAccountFixtures("2026-04-02T01:00:00Z")
	if err != nil {
		t.Fatalf("buildAccountFixtures() new error = %v", err)
	}

	readManagedFile = func(path string) ([]byte, error) {
		switch path {
		case testPeerPath():
			return []byte(`{}`), nil
		case testMetaPath():
			return buildSignedMetaFixture(t, originalPrivateKey, testJoiningNodeID, originalAccountID, testBootstrapTimestamp, 1), nil
		case filepath.Join("network", "accounts", originalAccountID+pubkeyFileSuffix):
			return originalPubKeyData, nil
		case filepath.Join("network", "accounts", originalAccountID+metaFileSuffix):
			return originalAccountMetaData, nil
		case filepath.Join("network", "accounts", originalAccountID+accountBlobFileSuffix):
			return originalBlobData, nil
		default:
			return nil, os.ErrNotExist
		}
	}

	request := bootstrapSessionFinalizeRequest{
		Type:          "finalize",
		NodeID:        testJoiningNodeID,
		AccountID:     newAccountID,
		PeerData:      buildSignedPeerFixture(t, newPrivateKey, testBootstrapHost, 58103, newAccountID),
		MetaData:      buildSignedMetaFixture(t, newPrivateKey, testJoiningNodeID, newAccountID, testBootstrapTimestamp, 2),
		AccountBlob:   newBlobData,
		AccountPubKey: newPubKeyData,
		AccountMeta:   newAccountMetaData,
	}

	if err := validateFinalizeRequest(request); err == nil {
		t.Fatal("validateFinalizeRequest() error = nil, want takeover rejection")
	}
}

func buildAccountFixtures(createdAt string) (string, ed25519.PrivateKey, []byte, []byte, []byte, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", nil, nil, nil, nil, err
	}

	accountID := accounts.AccountIDFromPublicKey(publicKey)
	blobData, err := accounts.BuildBlob(accountID, publicKey, privateKey, testAccountPassword)
	if err != nil {
		return "", nil, nil, nil, nil, err
	}
	accountPubKeyData := accounts.BuildPublicKeyFile(publicKey)
	accountMetaData, err := accounts.BuildMeta(accountID, publicKey, createdAt, 1, privateKey)
	if err != nil {
		return "", nil, nil, nil, nil, err
	}

	return accountID, privateKey, blobData, accountPubKeyData, accountMetaData, nil
}

func buildSignedMetaFixture(t *testing.T, privateKey ed25519.PrivateKey, nodeID, accountID, firstSeen string, revision int) []byte {
	t.Helper()

	data, err := buildMetaFile(nodeID, accountID, firstSeen, revision, privateKey)
	if err != nil {
		t.Fatalf("buildMetaFile() error = %v", err)
	}
	return data
}

func buildSignedPeerFixture(t *testing.T, privateKey ed25519.PrivateKey, ip string, port int, accountID string) []byte {
	t.Helper()

	data, err := buildPeerFile(ip, port, accountID, privateKey)
	if err != nil {
		t.Fatalf("buildPeerFile() error = %v", err)
	}
	return data
}

func testPeerPath() string {
	return filepath.Join("network", "peers", testJoiningPeerFile)
}

func testMetaPath() string {
	return filepath.Join("network", "peers", testJoiningMetaFile)
}

func testAccountKeyPath() string {
	return filepath.Join("local", "account", testAccountKeyFile)
}

func testAccountBlobPath() string {
	return filepath.Join("network", "accounts", testAccountBlobFile)
}

func testAccountPubKeyPath() string {
	return filepath.Join("network", "accounts", testAccountID+pubkeyFileSuffix)
}

func testAccountMetaPath() string {
	return filepath.Join("network", "accounts", testAccountID+metaFileSuffix)
}

func testBootstrapMaterial(t *testing.T) accounts.Material {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	return accounts.Material{
		AccountID:    testAccountID,
		LocalKeyPath: testAccountKeyPath(),
		PublicKey:    publicKey,
		PrivateKey:   privateKey,
		BlobData:     []byte(testAccountBlobJSON),
	}
}

func respondToFinalizeRequest(t *testing.T, serverConn net.Conn) {
	t.Helper()

	var finalizeRequest bootstrapSessionFinalizeRequest
	if err := json.NewDecoder(serverConn).Decode(&finalizeRequest); err != nil {
		t.Errorf(testDecodeErrorFormat, err)
		return
	}
	if finalizeRequest.NodeID != testJoiningNodeID {
		t.Errorf("finalizeRequest.NodeID = %q, want %q", finalizeRequest.NodeID, testJoiningNodeID)
	}
	if finalizeRequest.AccountID != testAccountID {
		t.Errorf("finalizeRequest.AccountID = %q, want %q", finalizeRequest.AccountID, testAccountID)
	}
	if len(finalizeRequest.AccountPubKey) == 0 {
		t.Error("finalizeRequest.AccountPubKey = empty, want populated data")
	}
	if len(finalizeRequest.AccountMeta) == 0 {
		t.Error("finalizeRequest.AccountMeta = empty, want populated data")
	}
	if err := json.NewEncoder(serverConn).Encode(bootstrapSessionFinalizeResponse{
		PeerFile:        testPeerPath(),
		MetaFile:        testMetaPath(),
		AccountBlobFile: testAccountBlobPath(),
	}); err != nil {
		t.Errorf(testEncodeErrorFormat, err)
	}
}

func assertCompleteResult(t *testing.T, result ConnectResult) {
	t.Helper()

	if !result.Connected {
		t.Fatal("Complete() Connected = false, want true")
	}
	if result.PeerFile != testPeerPath() {
		t.Fatalf("Complete() PeerFile = %q, want %q", result.PeerFile, testPeerPath())
	}
	if result.MetaFile != testMetaPath() {
		t.Fatalf("Complete() MetaFile = %q, want %q", result.MetaFile, testMetaPath())
	}
	if result.AccountBlobFile != testAccountBlobPath() {
		t.Fatalf("Complete() AccountBlobFile = %q, want %q", result.AccountBlobFile, testAccountBlobPath())
	}
	if result.LocalKeyFile != testAccountKeyPath() {
		t.Fatalf("Complete() LocalKeyFile = %q, want %q", result.LocalKeyFile, testAccountKeyPath())
	}
}

func assertBootstrapFilesWritten(t *testing.T, writtenFiles map[string][]byte) {
	t.Helper()

	requiredPaths := []string{
		testPeerPath(),
		testMetaPath(),
		testAccountBlobPath(),
		testAccountPubKeyPath(),
		testAccountMetaPath(),
	}

	for _, path := range requiredPaths {
		if _, ok := writtenFiles[path]; !ok {
			t.Fatalf("missing written bootstrap file: %s", path)
		}
	}
}

func loadBootstrapListFixture(t *testing.T) []byte {
	t.Helper()

	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed to resolve bootstrap test path")
	}

	bootstrapListPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "network", "bootstrap-list.yaml")
	data, err := os.ReadFile(bootstrapListPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", bootstrapListPath, err)
	}

	return data
}

func stubBootstrapHooks(t *testing.T) func() {
	t.Helper()

	originalEnsureDataLayout := ensureDataLayout
	originalReadDirectory := readDirectory
	originalReadManagedFile := readManagedFile
	originalWriteManagedFile := writeManagedFile
	originalResolveNodeID := resolveNodeID
	originalCreateAccount := createAccount
	originalRecoverAccount := recoverAccount
	originalHTTPClient := httpClient
	originalFetchRemoteList := fetchRemoteList
	originalProbeEndpoint := probeEndpoint
	originalDialBootstrap := dialBootstrap
	originalListenBootstrap := listenBootstrap
	originalCurrentTime := currentTime
	originalRandomSource := randomSource
	originalServerStartOnce := serverStartOnce
	originalPendingSessions := pendingSessions

	ensureDataLayout = datamanager.EnsureLayout
	readDirectory = os.ReadDir
	readManagedFile = datamanager.ReadFile
	writeManagedFile = datamanager.WriteFile
	resolveNodeID = nodeid.GetNodeID
	createAccount = accounts.Generate
	recoverAccount = accounts.Recover
	httpClient = &http.Client{Timeout: 5 * time.Second}
	fetchRemoteList = fetchRemoteBootstrapList
	probeEndpoint = measureEndpointLatency
	dialBootstrap = dialBootstrapEndpoint
	listenBootstrap = listenBootstrapEndpoint
	currentTime = time.Now
	randomSource = rand.Reader
	serverStartOnce = sync.Once{}
	pendingSessions = map[string]*pendingSession{}

	return func() {
		clearPendingSessions()
		ensureDataLayout = originalEnsureDataLayout
		readDirectory = originalReadDirectory
		readManagedFile = originalReadManagedFile
		writeManagedFile = originalWriteManagedFile
		resolveNodeID = originalResolveNodeID
		createAccount = originalCreateAccount
		recoverAccount = originalRecoverAccount
		httpClient = originalHTTPClient
		fetchRemoteList = originalFetchRemoteList
		probeEndpoint = originalProbeEndpoint
		dialBootstrap = originalDialBootstrap
		listenBootstrap = originalListenBootstrap
		currentTime = originalCurrentTime
		randomSource = originalRandomSource
		serverStartOnce = originalServerStartOnce
		pendingSessions = originalPendingSessions
	}
}

type failingListener struct {
	acceptErr error
}

func (l *failingListener) Accept() (net.Conn, error) {
	return nil, l.acceptErr
}

func (l *failingListener) Close() error {
	return nil
}

func (l *failingListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4zero, Port: 58103}
}

type connWithRemoteAddr struct {
	net.Conn
	remoteAddr net.Addr
}

func (c connWithRemoteAddr) RemoteAddr() net.Addr {
	return c.remoteAddr
}
