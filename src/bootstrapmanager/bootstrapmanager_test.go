package bootstrapmanager

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"continuum/src/accounts"
	"continuum/src/datamanager"
	"continuum/src/nodeid"
)

const sampleBootstrapYAML = `version: 1
nodes:
  na-west:
    node_id: "west-node"
    host: "203.0.113.50"
    port: 58103
  na-east:
    node_id: "east-node"
    host: "162.191.52.239"
    port: 58103
`

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
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return []byte(sampleBootstrapYAML), nil
	}
	probeEndpoint = func(host string, port int) (time.Duration, error) {
		switch host {
		case "162.191.52.239":
			return 12 * time.Millisecond, nil
		case "203.0.113.50":
			return 34 * time.Millisecond, nil
		default:
			t.Fatalf("probeEndpoint() host = %q, want known host", host)
			return 0, nil
		}
	}

	state := LoadState()
	if !state.NeedsBootstrap {
		t.Fatal("LoadState() NeedsBootstrap = false, want true")
	}
	if len(state.Nodes) != 2 {
		t.Fatalf("len(LoadState().Nodes) = %d, want %d", len(state.Nodes), 2)
	}
	if state.Nodes[0].Name != "na-east" || state.Nodes[0].LatencyMilliseconds != 12 {
		t.Fatalf("LoadState().Nodes[0] = %#v, want na-east first with 12ms", state.Nodes[0])
	}
	if state.Nodes[1].Name != "na-west" || state.Nodes[1].LatencyMilliseconds != 34 {
		t.Fatalf("LoadState().Nodes[1] = %#v, want na-west second with 34ms", state.Nodes[1])
	}
}

func TestBuildBootstrapStartResponseUsesObservedIPv4AndRecovery(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, privateKey, blobData, accountPubKeyData, accountMetaData, err := buildAccountFixtures("2026-04-02T00:00:00Z")
	if err != nil {
		t.Fatalf("buildAccountFixtures() error = %v", err)
	}

	probeEndpoint = func(string, int) (time.Duration, error) {
		return 5 * time.Millisecond, nil
	}
	readManagedFile = func(path string) ([]byte, error) {
		switch path {
		case filepath.Join("network", "peers", "joining-node.peer"):
			return []byte(`{}`), nil
		case filepath.Join("network", "peers", "joining-node.meta"):
			return buildSignedMetaFixture(t, privateKey, "joining-node", accountID, "2026-04-02T00:00:00Z", 7), nil
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

	response := buildBootstrapStartResponse(&net.TCPAddr{IP: net.ParseIP("162.191.52.239"), Port: 43001}, bootstrapSessionStartRequest{
		Type:       "start",
		NodeID:     "joining-node",
		ListenPort: 58103,
	})

	if response.ObservedIPv4 != "162.191.52.239" {
		t.Fatalf("buildBootstrapStartResponse().ObservedIPv4 = %q, want %q", response.ObservedIPv4, "162.191.52.239")
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
	if response.FirstSeen != "2026-04-02T00:00:00Z" {
		t.Fatalf("buildBootstrapStartResponse().FirstSeen = %q, want %q", response.FirstSeen, "2026-04-02T00:00:00Z")
	}
	if response.Revision != 8 {
		t.Fatalf("buildBootstrapStartResponse().Revision = %d, want %d", response.Revision, 8)
	}
	if response.AccountCreatedAt != "2026-04-02T00:00:00Z" {
		t.Fatalf("buildBootstrapStartResponse().AccountCreatedAt = %q, want %q", response.AccountCreatedAt, "2026-04-02T00:00:00Z")
	}
	if response.AccountRevision != 2 {
		t.Fatalf("buildBootstrapStartResponse().AccountRevision = %d, want %d", response.AccountRevision, 2)
	}
}

func TestConnectReturnsAwaitingPasswordSession(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string {
		return "joining-node"
	}
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return []byte(sampleBootstrapYAML), nil
	}

	serverConn, clientConn := net.Pipe()
	dialBootstrap = func(context.Context, string, int) (net.Conn, error) {
		return clientConn, nil
	}

	go func() {
		defer serverConn.Close()

		var request bootstrapSessionStartRequest
		if err := json.NewDecoder(serverConn).Decode(&request); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		if request.NodeID != "joining-node" {
			t.Errorf("request.NodeID = %q, want %q", request.NodeID, "joining-node")
		}

		if err := json.NewEncoder(serverConn).Encode(bootstrapSessionStartResponse{
			ObservedIPv4:      "162.191.52.239",
			Port:              58103,
			Reachable:         true,
			RecoveryAvailable: true,
			AccountID:         "account-123",
			FirstSeen:         "2026-04-02T00:00:00Z",
			Revision:          4,
		}); err != nil {
			t.Errorf("Encode() error = %v", err)
		}
	}()

	result, err := Connect("162.191.52.239", 58103, "")
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
	if result.AccountID != "account-123" {
		t.Fatalf("Connect() AccountID = %q, want %q", result.AccountID, "account-123")
	}

	removePendingSession(result.SessionID)
}

func TestCompleteCreatesFilesAndFinalizesSession(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	createAccount = func(password string) (accounts.Material, error) {
		if password != "secret-pass" {
			t.Fatalf("createAccount() password = %q, want %q", password, "secret-pass")
		}
		return accounts.Material{
			AccountID:    "account-123",
			LocalKeyPath: filepath.Join("local", "account", "account-123.key"),
			PublicKey:    publicKey,
			PrivateKey:   privateKey,
			BlobData:     []byte(`{"AccountID":"account-123"}`),
		}, nil
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	session := &pendingSession{
		id:     "session-123",
		conn:   clientConn,
		nodeID: "joining-node",
		response: bootstrapSessionStartResponse{
			ObservedIPv4: "162.191.52.239",
			Port:         58103,
			Reachable:    true,
			FirstSeen:    "2026-04-02T00:00:00Z",
			Revision:     1,
		},
	}
	storePendingSession(session)
	t.Cleanup(func() {
		removePendingSession("session-123")
	})

	writtenFiles := map[string][]byte{}
	writeManagedFile = func(path string, data []byte, perm os.FileMode) error {
		writtenFiles[path] = append([]byte(nil), data...)
		return nil
	}

	go func() {
		var finalizeRequest bootstrapSessionFinalizeRequest
		if err := json.NewDecoder(serverConn).Decode(&finalizeRequest); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		if finalizeRequest.NodeID != "joining-node" {
			t.Errorf("finalizeRequest.NodeID = %q, want %q", finalizeRequest.NodeID, "joining-node")
		}
		if finalizeRequest.AccountID != "account-123" {
			t.Errorf("finalizeRequest.AccountID = %q, want %q", finalizeRequest.AccountID, "account-123")
		}
		if len(finalizeRequest.AccountPubKey) == 0 {
			t.Error("finalizeRequest.AccountPubKey = empty, want populated data")
		}
		if len(finalizeRequest.AccountMeta) == 0 {
			t.Error("finalizeRequest.AccountMeta = empty, want populated data")
		}
		if err := json.NewEncoder(serverConn).Encode(bootstrapSessionFinalizeResponse{
			PeerFile:        filepath.Join("network", "peers", "joining-node.peer"),
			MetaFile:        filepath.Join("network", "peers", "joining-node.meta"),
			AccountBlobFile: filepath.Join("network", "accounts", "account-123.blob"),
		}); err != nil {
			t.Errorf("Encode() error = %v", err)
		}
	}()

	result, err := Complete("session-123", "secret-pass")
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if !result.Connected {
		t.Fatal("Complete() Connected = false, want true")
	}
	if result.PeerFile != filepath.Join("network", "peers", "joining-node.peer") {
		t.Fatalf("Complete() PeerFile = %q, want %q", result.PeerFile, filepath.Join("network", "peers", "joining-node.peer"))
	}
	if result.MetaFile != filepath.Join("network", "peers", "joining-node.meta") {
		t.Fatalf("Complete() MetaFile = %q, want %q", result.MetaFile, filepath.Join("network", "peers", "joining-node.meta"))
	}
	if result.AccountBlobFile != filepath.Join("network", "accounts", "account-123.blob") {
		t.Fatalf("Complete() AccountBlobFile = %q, want %q", result.AccountBlobFile, filepath.Join("network", "accounts", "account-123.blob"))
	}
	if result.LocalKeyFile != filepath.Join("local", "account", "account-123.key") {
		t.Fatalf("Complete() LocalKeyFile = %q, want %q", result.LocalKeyFile, filepath.Join("local", "account", "account-123.key"))
	}
	if _, ok := writtenFiles[filepath.Join("network", "peers", "joining-node.peer")]; !ok {
		t.Fatal("Complete() did not write the peer file locally")
	}
	if _, ok := writtenFiles[filepath.Join("network", "peers", "joining-node.meta")]; !ok {
		t.Fatal("Complete() did not write the meta file locally")
	}
	if _, ok := writtenFiles[filepath.Join("network", "accounts", "account-123.blob")]; !ok {
		t.Fatal("Complete() did not write the account blob locally")
	}
	if _, ok := writtenFiles[filepath.Join("network", "accounts", "account-123.pubkey")]; !ok {
		t.Fatal("Complete() did not write the account pubkey locally")
	}
	if _, ok := writtenFiles[filepath.Join("network", "accounts", "account-123.meta")]; !ok {
		t.Fatal("Complete() did not write the account meta locally")
	}
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
		if password != "recovery-pass" {
			t.Fatalf("recoverAccount() password = %q, want %q", password, "recovery-pass")
		}
		return accounts.Material{
			AccountID:    "account-123",
			LocalKeyPath: filepath.Join("local", "account", "account-123.key"),
			PublicKey:    publicKey,
			PrivateKey:   privateKey,
			BlobData:     []byte(`{"blob":"data"}`),
		}, nil
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	session := &pendingSession{
		id:     "session-recover",
		conn:   clientConn,
		nodeID: "joining-node",
		response: bootstrapSessionStartResponse{
			ObservedIPv4:      "162.191.52.239",
			Port:              58103,
			Reachable:         false,
			RecoveryAvailable: true,
			AccountID:         "account-123",
			AccountBlob:       []byte(`{"blob":"data"}`),
			FirstSeen:         "2026-04-02T00:00:00Z",
			Revision:          9,
		},
	}
	storePendingSession(session)
	t.Cleanup(func() {
		removePendingSession("session-recover")
	})

	writeManagedFile = func(string, []byte, os.FileMode) error { return nil }
	go func() {
		var finalizeRequest bootstrapSessionFinalizeRequest
		if err := json.NewDecoder(serverConn).Decode(&finalizeRequest); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		if err := json.NewEncoder(serverConn).Encode(bootstrapSessionFinalizeResponse{}); err != nil {
			t.Errorf("Encode() error = %v", err)
		}
	}()

	result, err := Complete("session-recover", "recovery-pass")
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

	originalAccountID, originalPrivateKey, originalBlobData, originalPubKeyData, originalAccountMetaData, err := buildAccountFixtures("2026-04-02T00:00:00Z")
	if err != nil {
		t.Fatalf("buildAccountFixtures() original error = %v", err)
	}
	newAccountID, newPrivateKey, newBlobData, newPubKeyData, newAccountMetaData, err := buildAccountFixtures("2026-04-02T01:00:00Z")
	if err != nil {
		t.Fatalf("buildAccountFixtures() new error = %v", err)
	}

	readManagedFile = func(path string) ([]byte, error) {
		switch path {
		case filepath.Join("network", "peers", "joining-node.peer"):
			return []byte(`{}`), nil
		case filepath.Join("network", "peers", "joining-node.meta"):
			return buildSignedMetaFixture(t, originalPrivateKey, "joining-node", originalAccountID, "2026-04-02T00:00:00Z", 1), nil
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
		NodeID:        "joining-node",
		AccountID:     newAccountID,
		PeerData:      buildSignedPeerFixture(t, newPrivateKey, "162.191.52.239", 58103, newAccountID),
		MetaData:      buildSignedMetaFixture(t, newPrivateKey, "joining-node", newAccountID, "2026-04-02T00:00:00Z", 2),
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
	blobData, err := accounts.BuildBlob(accountID, publicKey, privateKey, "secret-pass")
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
