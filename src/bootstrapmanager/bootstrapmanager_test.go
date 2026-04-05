package bootstrapmanager

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"continuum/src/accounts"
	"continuum/src/datamanager"
	"continuum/src/networkmanager"
	"continuum/src/nodeid"
)

const (
	testBootstrapHost                        = "162.191.52.239"
	testLoopbackHost                         = "127.0.0.1"
	testBootstrapName                        = "na-east"
	testBootstrapTimestamp                   = "2026-04-02T00:00:00Z"
	testUpdatedTimestamp                     = "2026-04-02T01:00:00Z"
	testJoiningNodeID                        = "joining-node"
	testJoiningPeerFile                      = "joining-node.peer"
	testJoiningMetaFile                      = "joining-node.meta"
	testDecodeErrorFormat                    = "Decode() error = %v"
	testEncodeErrorFormat                    = "Encode() error = %v"
	testEncodeStartRequestErrorFormat        = "Encode(start request) error = %v"
	testDecodeStartResponseErrorFormat       = "Decode(start response) error = %v"
	testEmptyStartResponseErrorFormat        = "startResponse.Error = %q, want empty"
	testEncodeFinalizeRequestErrorFormat     = "Encode(finalize request) error = %v"
	testDecodeFinalizeResponseErrorFormat    = "Decode(finalize response) error = %v"
	testMarshalErrorFormat                   = "Marshal() error = %v"
	testWriteErrorFormat                     = "Write() error = %v"
	testMkdirAllErrorFormat                  = "MkdirAll() error = %v"
	testWriteFileErrorFormat                 = "WriteFile() error = %v"
	testLoadStateNeedsBootstrapText          = "LoadState() NeedsBootstrap = false, want true"
	testBuildAccountFixturesErrorFormat      = "buildAccountFixtures() error = %v"
	testBuildAccountFixturesOtherErrorFormat = "buildAccountFixtures() other error = %v"
	testConnectErrorFormat                   = "Connect() error = %v"
	testConnectWantErrorFormat               = "Connect() error = %v, want %v"
	testCompleteWantErrorFormat              = "Complete() error = %v, want %s"
	testRegisterWantErrorFormat              = "Register() error = %v, want %v"
	testLoginWantErrorFormat                 = "Login() error = %v, want %v"
	testSaveLocalProfileArgsFormat           = "saveLocalProfile() args = (%q, %q), want (%q, %q)"
	testGenerateKeyErrorFormat               = "GenerateKey() error = %v"
	testDefaultListenPortFormat              = "defaultListenPort() = %d, want %d"
	testLoadExistingNodeRecordsErrorFormat   = "loadExistingNodeRecords() error = %v"
	testLoadExistingAccountRecordsWantErrFmt = "loadExistingAccountRecords() error = %v, want %v"
	testVerifyPublicKeyFileErrorFormat       = "VerifyPublicKeyFile() error = %v"
	testAccountID                            = "account-123"
	testAccountPassword                      = "secret-pass"
	testAccountKeyFile                       = "account-123.key"
	testAccountBlobFile                      = "account-123.blob"
	testAccountBlobJSON                      = `{"AccountID":"account-123"}`
	testSessionID                            = "session-123"
	testRecoveryPassword                     = "recovery-pass"
	testRecoverySessionID                    = "session-recover"
	testBootstrapNodeID                      = "264648e40c71d6385d470ca4c8e5156a1abb74af6aa1e92a948066139a5b5e45"
	testOtherAccountID                       = "other-account"
	testFirstIndexedAccountID                = "account-1"
	testSecondIndexedAccountID               = "account-2"
	testAliceHashKey                         = "hash-alice"
	testInvalidJSON                          = "not-json"
	testInvalidBase64                        = "not-base64"
	testWriteFailedText                      = "write failed"
	testSignFailedText                       = "sign failed"
	testFinalizeFailedText                   = "finalize failed"
	testPeerReadFailedText                   = "peer read failed"
	testLayoutErrorText                      = "layout failed"
	testReadDirErrorText                     = "readdir failed"
	testLoadNodesErrorText                   = "bootstrap load failed"
	testSessionMissingText                   = "bootstrap session expired or was not found"
)

func TestLoadStateReturnsExistingPeerCount(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	dataPath := filepath.Join(t.TempDir(), "data")
	peersPath := filepath.Join(dataPath, "network", "peers")
	if err := os.MkdirAll(peersPath, 0o755); err != nil {
		t.Fatalf(testMkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(filepath.Join(peersPath, "known-node.peer"), []byte("{}"), 0o644); err != nil {
		t.Fatalf(testWriteFileErrorFormat, err)
	}
	if err := os.WriteFile(filepath.Join(peersPath, "notes.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatalf(testWriteFileErrorFormat, err)
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

func TestLoadStateReturnsLayoutError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	ensureDataLayout = func() (string, error) {
		return "", errors.New(testLayoutErrorText)
	}

	state := LoadState()
	if !state.NeedsBootstrap {
		t.Fatal(testLoadStateNeedsBootstrapText)
	}
	if !strings.Contains(state.Error, testLayoutErrorText) {
		t.Fatalf("LoadState() Error = %q, want layout error", state.Error)
	}
}

func TestLoadStateReturnsPeerInspectError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	dataPath := filepath.Join(t.TempDir(), "data")
	ensureDataLayout = func() (string, error) {
		return dataPath, nil
	}
	readDirectory = func(string) ([]os.DirEntry, error) {
		return nil, errors.New(testReadDirErrorText)
	}

	state := LoadState()
	if !state.NeedsBootstrap {
		t.Fatal(testLoadStateNeedsBootstrapText)
	}
	if !strings.Contains(state.Error, testReadDirErrorText) {
		t.Fatalf("LoadState() Error = %q, want inspect error", state.Error)
	}
}

func TestLoadStateReturnsBootstrapNodeLoadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	dataPath := filepath.Join(t.TempDir(), "data")
	peersPath := filepath.Join(dataPath, "network", "peers")
	if err := os.MkdirAll(peersPath, 0o755); err != nil {
		t.Fatalf(testMkdirAllErrorFormat, err)
	}
	ensureDataLayout = func() (string, error) {
		return dataPath, nil
	}
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return nil, errors.New(testLoadNodesErrorText)
	}

	state := LoadState()
	if !state.NeedsBootstrap {
		t.Fatal(testLoadStateNeedsBootstrapText)
	}
	if !strings.Contains(state.Error, testLoadNodesErrorText) {
		t.Fatalf("LoadState() Error = %q, want load-nodes error", state.Error)
	}
}

func TestLoadStateFetchesAndSortsBootstrapNodes(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	dataPath := filepath.Join(t.TempDir(), "data")
	peersPath := filepath.Join(dataPath, "network", "peers")
	if err := os.MkdirAll(peersPath, 0o755); err != nil {
		t.Fatalf(testMkdirAllErrorFormat, err)
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
		t.Fatal(testLoadStateNeedsBootstrapText)
	}
	if len(state.Nodes) != 1 {
		t.Fatalf("len(LoadState().Nodes) = %d, want %d", len(state.Nodes), 1)
	}
	if state.Nodes[0].Name != testBootstrapName || state.Nodes[0].LatencyMilliseconds != 12 {
		t.Fatalf("LoadState().Nodes[0] = %#v, want %s first with 12ms", state.Nodes[0], testBootstrapName)
	}
}

func TestBuildBootstrapStartResponseUsesObservedIPv4AndRecovery(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, privateKey, blobData, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
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
	if len(response.RecoveryBundle) != 5 {
		t.Fatalf("len(buildBootstrapStartResponse().RecoveryBundle) = %d, want %d", len(response.RecoveryBundle), 5)
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

func TestBuildBootstrapStartResponsePropagatesUsernameHash(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, privateKey, blobData, accountPubKeyData, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	accountPubKey := privateKey.Public().(ed25519.PublicKey)
	accountMetaData, err := accounts.BuildMetaWithUsernameHash(accountID, accountPubKey, testBootstrapTimestamp, 1, testAliceHashKey, privateKey)
	if err != nil {
		t.Fatalf("BuildMetaWithUsernameHash() error = %v", err)
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
	if response.UsernameHash != testAliceHashKey {
		t.Fatalf("buildBootstrapStartResponse().UsernameHash = %q, want %q", response.UsernameHash, testAliceHashKey)
	}
}

func TestCompletionMaterialUsesRecoveryBlob(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantMaterial := accounts.Material{AccountID: testAccountID}
	recoverAccount = func(blobData []byte, password string) (accounts.Material, error) {
		if string(blobData) != testAccountBlobJSON {
			t.Fatalf("recoverAccount() blobData = %s, want %s", string(blobData), testAccountBlobJSON)
		}
		if password != testRecoveryPassword {
			t.Fatalf("recoverAccount() password = %q, want %q", password, testRecoveryPassword)
		}
		return wantMaterial, nil
	}

	got, err := completionMaterial(bootstrapSessionStartResponse{
		RecoveryAvailable: true,
		AccountBlob:       []byte(testAccountBlobJSON),
	}, testRecoveryPassword)
	if err != nil {
		t.Fatalf("completionMaterial() error = %v", err)
	}
	if got.AccountID != wantMaterial.AccountID {
		t.Fatalf("completionMaterial().AccountID = %q, want %q", got.AccountID, wantMaterial.AccountID)
	}
}

func TestMetaFileUnmarshalJSONReturnsCurrentFieldError(t *testing.T) {
	var file metaFile
	if err := file.UnmarshalJSON([]byte(`{"first_seen":1}`)); err == nil {
		t.Fatal("metaFile.UnmarshalJSON() error = nil, want current field decode failure")
	}
}

func TestMetaFileUnmarshalJSONReturnsLegacyFieldError(t *testing.T) {
	var file metaFile
	if err := file.UnmarshalJSON([]byte(`{"First Seen":1}`)); err == nil {
		t.Fatal("metaFile.UnmarshalJSON() error = nil, want legacy field decode failure")
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
		t.Fatalf(testConnectErrorFormat, err)
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

func TestPeerFileCountReturnsReadDirectoryError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New(testReadDirErrorText)
	readDirectory = func(string) ([]os.DirEntry, error) {
		return nil, wantErr
	}

	_, err := peerFileCount(filepath.Join(t.TempDir(), "peers"))
	if !errors.Is(err, wantErr) {
		t.Fatalf("peerFileCount() error = %v, want %v", err, wantErr)
	}
}

func TestPeerFileCountSkipsDirectories(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	peersPath := filepath.Join(t.TempDir(), "peers")
	if err := os.MkdirAll(filepath.Join(peersPath, "nested"), 0o755); err != nil {
		t.Fatalf(testMkdirAllErrorFormat, err)
	}
	if err := os.WriteFile(filepath.Join(peersPath, "known.peer"), []byte("{}"), 0o644); err != nil {
		t.Fatalf(testWriteFileErrorFormat, err)
	}

	count, err := peerFileCount(peersPath)
	if err != nil {
		t.Fatalf("peerFileCount() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("peerFileCount() = %d, want %d", count, 1)
	}
}

func TestLoadBootstrapNodesReturnsListError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return nil, errors.New(testLoadNodesErrorText)
	}

	_, err := loadBootstrapNodes(context.Background())
	if err == nil || !strings.Contains(err.Error(), testLoadNodesErrorText) {
		t.Fatalf("loadBootstrapNodes() error = %v, want bootstrap list error", err)
	}
}

func TestProbeBootstrapNodeMarksInvalidEndpoint(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	node := Node{Name: "invalid"}
	probeBootstrapNode(&node, "")
	if node.Error != "invalid endpoint" {
		t.Fatalf("probeBootstrapNode() Error = %q, want %q", node.Error, "invalid endpoint")
	}
	if node.Reachable {
		t.Fatal("probeBootstrapNode() Reachable = true, want false")
	}
}

func TestProbeBootstrapNodeStoresProbeError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New("probe failed")
	probeEndpoint = func(string, int) (time.Duration, error) {
		return 0, wantErr
	}

	node := Node{Name: testBootstrapName, Host: testBootstrapHost, Port: 58103}
	probeBootstrapNode(&node, "")
	if node.Error != wantErr.Error() {
		t.Fatalf("probeBootstrapNode() Error = %q, want %q", node.Error, wantErr.Error())
	}
	if node.Reachable {
		t.Fatal("probeBootstrapNode() Reachable = true, want false")
	}
}

func TestBootstrapProbeHostUsesLoopbackForLocalNode(t *testing.T) {
	if got := bootstrapProbeHost(Node{NodeID: testJoiningNodeID, Host: testBootstrapHost}, testJoiningNodeID); got != testLoopbackHost {
		t.Fatalf("bootstrapProbeHost() = %q, want %q", got, testLoopbackHost)
	}
}

func TestSortBootstrapNodesOrdersByReachabilityLatencyAndName(t *testing.T) {
	nodes := []Node{
		{Name: "zeta", Reachable: false},
		{Name: "bravo", Reachable: true, LatencyMilliseconds: 50},
		{Name: "alpha", Reachable: true, LatencyMilliseconds: 10},
		{Name: "charlie", Reachable: true, LatencyMilliseconds: 10},
	}

	sortBootstrapNodes(nodes)

	gotNames := []string{nodes[0].Name, nodes[1].Name, nodes[2].Name, nodes[3].Name}
	wantNames := []string{"alpha", "charlie", "bravo", "zeta"}
	if fmt.Sprint(gotNames) != fmt.Sprint(wantNames) {
		t.Fatalf("sortBootstrapNodes() names = %v, want %v", gotNames, wantNames)
	}
}

func TestLoadBootstrapListReturnsFetchError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New(testLoadNodesErrorText)
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return nil, wantErr
	}

	_, err := loadBootstrapList(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("loadBootstrapList() error = %v, want %v", err, wantErr)
	}
}

func TestLoadBootstrapListReturnsYAMLError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return []byte("{not-yaml"), nil
	}

	if _, err := loadBootstrapList(context.Background()); err == nil {
		t.Fatal("loadBootstrapList() error = nil, want YAML failure")
	}
}

func TestFetchRemoteBootstrapListReturnsRequestError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	if _, err := fetchRemoteBootstrapList(context.Background(), "://bad url"); err == nil {
		t.Fatal("fetchRemoteBootstrapList() error = nil, want request creation failure")
	}
}

func TestFetchRemoteBootstrapListReturnsHTTPStatusError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	defer server.Close()

	httpClient = server.Client()
	if _, err := fetchRemoteBootstrapList(context.Background(), server.URL); err == nil {
		t.Fatal("fetchRemoteBootstrapList() error = nil, want HTTP status failure")
	}
}

func TestFetchRemoteBootstrapListReturnsClientError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New("request failed")
	httpClient = &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, wantErr
		}),
	}

	if _, err := fetchRemoteBootstrapList(context.Background(), "https://example.com/bootstrap.yaml"); !errors.Is(err, wantErr) {
		t.Fatalf("fetchRemoteBootstrapList() error = %v, want %v", err, wantErr)
	}
}

func TestMeasureEndpointLatencyReturnsDialError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	listener, err := net.Listen("tcp4", testLoopbackHost+":0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	if _, err := measureEndpointLatency(testLoopbackHost, port); err == nil {
		t.Fatal("measureEndpointLatency() error = nil, want dial failure")
	}
}

func TestMeasureEndpointLatency(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	listener, err := net.Listen("tcp4", testLoopbackHost+":0")
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
	latency, err := measureEndpointLatency(testLoopbackHost, port)
	if err != nil {
		t.Fatalf("measureEndpointLatency() error = %v", err)
	}
	if latency < 0 {
		t.Fatalf("measureEndpointLatency() = %s, want >= 0", latency)
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
	conn, err := dialBootstrapEndpoint(context.Background(), testLoopbackHost, port)
	if err != nil {
		t.Fatalf("dialBootstrapEndpoint() error = %v", err)
	}
	_ = conn.Close()

	if err := <-done; err != nil {
		t.Fatalf("listener.Accept() error = %v", err)
	}
}

func TestConnectRejectsInvalidEndpoint(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	if _, err := Connect("", 0, ""); !errors.Is(err, errInvalidBootstrapEndpoint) {
		t.Fatalf(testConnectWantErrorFormat, err, errInvalidBootstrapEndpoint)
	}
}

func TestConnectReturnsLocalNodeIDError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return "" }

	if _, err := Connect(testBootstrapHost, 58103, ""); err == nil {
		t.Fatal("Connect() error = nil, want local node id failure")
	}
}

func TestConnectReturnsDialError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testJoiningNodeID }
	wantErr := errors.New("dial failed")
	dialBootstrap = func(context.Context, string, int) (net.Conn, error) {
		return nil, wantErr
	}

	if _, err := Connect(testBootstrapHost, 58103, ""); !errors.Is(err, wantErr) {
		t.Fatalf(testConnectWantErrorFormat, err, wantErr)
	}
}

func TestConnectUsesLoopbackForLocalBootstrapNode(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testJoiningNodeID }
	dialedHost := ""
	serverConn, clientConn := net.Pipe()
	dialBootstrap = func(_ context.Context, host string, port int) (net.Conn, error) {
		dialedHost = host
		if port != 58103 {
			t.Fatalf("dialBootstrap() port = %d, want %d", port, 58103)
		}
		return clientConn, nil
	}

	go func() {
		defer serverConn.Close()
		var request bootstrapSessionStartRequest
		if err := json.NewDecoder(serverConn).Decode(&request); err != nil {
			t.Errorf(testDecodeErrorFormat, err)
			return
		}
		_ = json.NewEncoder(serverConn).Encode(bootstrapSessionStartResponse{
			ObservedIPv4: testBootstrapHost,
			Port:         58103,
			Reachable:    true,
		})
	}()

	result, err := Connect(testBootstrapHost, 58103, testJoiningNodeID)
	if err != nil {
		t.Fatalf(testConnectErrorFormat, err)
	}
	if dialedHost != testLoopbackHost {
		t.Fatalf("Connect() dialed host = %q, want %q", dialedHost, testLoopbackHost)
	}

	removePendingSession(result.SessionID)
}

func TestConnectWrapsTrackedBootstrapConnection(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testJoiningNodeID }
	serverConn, clientConn := net.Pipe()
	dialBootstrap = func(context.Context, string, int) (net.Conn, error) {
		return clientConn, nil
	}

	var wrappedConn net.Conn
	wrapBootstrapConn = func(conn net.Conn) net.Conn {
		wrappedConn = conn
		return conn
	}

	go func() {
		defer serverConn.Close()
		var request bootstrapSessionStartRequest
		if err := json.NewDecoder(serverConn).Decode(&request); err != nil {
			t.Errorf(testDecodeErrorFormat, err)
			return
		}
		if err := json.NewEncoder(serverConn).Encode(bootstrapSessionStartResponse{
			ObservedIPv4: testBootstrapHost,
			Port:         58103,
			Reachable:    true,
		}); err != nil {
			t.Errorf(testEncodeErrorFormat, err)
		}
	}()

	result, err := Connect(testBootstrapHost, 58103, "")
	if err != nil {
		t.Fatalf(testConnectErrorFormat, err)
	}
	if wrappedConn == nil {
		t.Fatal("Connect() did not wrap the bootstrap connection")
	}

	removePendingSession(result.SessionID)
}

func TestConnectReturnsDeadlineError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testJoiningNodeID }
	wantErr := errors.New("deadline failed")
	dialBootstrap = func(context.Context, string, int) (net.Conn, error) {
		return &stubConn{deadlineErr: wantErr}, nil
	}

	if _, err := Connect(testBootstrapHost, 58103, ""); !errors.Is(err, wantErr) {
		t.Fatalf(testConnectWantErrorFormat, err, wantErr)
	}
}

func TestConnectReturnsEncodeError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testJoiningNodeID }
	wantErr := errors.New(testWriteFailedText)
	dialBootstrap = func(context.Context, string, int) (net.Conn, error) {
		return &stubConn{writeErr: wantErr}, nil
	}

	if _, err := Connect(testBootstrapHost, 58103, ""); !errors.Is(err, wantErr) {
		t.Fatalf(testConnectWantErrorFormat, err, wantErr)
	}
}

func TestConnectReturnsDecodeError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testJoiningNodeID }
	dialBootstrap = func(context.Context, string, int) (net.Conn, error) {
		return &stubConn{readData: []byte(testInvalidJSON)}, nil
	}

	if _, err := Connect(testBootstrapHost, 58103, ""); err == nil {
		t.Fatal("Connect() error = nil, want decode failure")
	}
}

func TestConnectReturnsBootstrapResponseError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testJoiningNodeID }
	responseData, err := json.Marshal(bootstrapSessionStartResponse{Error: "bootstrap rejected"})
	if err != nil {
		t.Fatalf(testMarshalErrorFormat, err)
	}
	dialBootstrap = func(context.Context, string, int) (net.Conn, error) {
		return &stubConn{readData: responseData}, nil
	}

	if _, err := Connect(testBootstrapHost, 58103, ""); err == nil || err.Error() != "bootstrap rejected" {
		t.Fatalf("Connect() error = %v, want bootstrap rejected", err)
	}
}

func TestConnectRejectsUnusableEndpointResponse(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testJoiningNodeID }
	responseData, err := json.Marshal(bootstrapSessionStartResponse{ObservedIPv4: "", Port: 0})
	if err != nil {
		t.Fatalf(testMarshalErrorFormat, err)
	}
	dialBootstrap = func(context.Context, string, int) (net.Conn, error) {
		return &stubConn{readData: responseData}, nil
	}

	if _, err := Connect(testBootstrapHost, 58103, ""); err == nil {
		t.Fatal("Connect() error = nil, want unusable endpoint failure")
	}
}

func TestConnectReturnsSessionIDError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testJoiningNodeID }
	responseData, err := json.Marshal(bootstrapSessionStartResponse{ObservedIPv4: testBootstrapHost, Port: 58103})
	if err != nil {
		t.Fatalf(testMarshalErrorFormat, err)
	}
	dialBootstrap = func(context.Context, string, int) (net.Conn, error) {
		return &stubConn{readData: responseData}, nil
	}
	randomSource = errReader{err: errors.New("random failed")}

	if _, err := Connect(testBootstrapHost, 58103, ""); err == nil {
		t.Fatal("Connect() error = nil, want session id failure")
	}
}

func TestConnectRemovesPendingSessionWhenTimerFires(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testJoiningNodeID }
	var scheduled func()
	scheduleAfter = func(_ time.Duration, fn func()) *time.Timer {
		scheduled = fn
		return time.NewTimer(time.Hour)
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
		if err := json.NewEncoder(serverConn).Encode(bootstrapSessionStartResponse{
			ObservedIPv4: testBootstrapHost,
			Port:         58103,
		}); err != nil {
			t.Errorf(testEncodeErrorFormat, err)
		}
	}()

	result, err := Connect(testBootstrapHost, 58103, "")
	if err != nil {
		t.Fatalf(testConnectErrorFormat, err)
	}
	if scheduled == nil {
		t.Fatal("Connect() did not schedule session timeout cleanup")
	}
	if _, ok := getPendingSession(result.SessionID); !ok {
		t.Fatal("Connect() did not store pending session before timeout")
	}

	scheduled()
	if _, ok := getPendingSession(result.SessionID); ok {
		t.Fatal("Connect() left pending session after timeout callback")
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

func TestStartBootstrapServiceReturnsLoadListError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return nil, errors.New(testLoadNodesErrorText)
	}

	if err := startBootstrapService(context.Background()); err == nil {
		t.Fatal("startBootstrapService() error = nil, want list load failure")
	}
}

func TestStartBootstrapServiceNoOpWithoutLocalNodeID(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return "" }
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return loadBootstrapListFixture(t), nil
	}
	listenBootstrap = func(int) (net.Listener, error) {
		t.Fatal("listenBootstrap() called without local node id")
		return nil, nil
	}

	if err := startBootstrapService(context.Background()); err != nil {
		t.Fatalf("startBootstrapService() error = %v", err)
	}
}

func TestStartBootstrapServiceNoOpWhenLocalNodeIsNotBootstrap(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return "not-a-bootstrap-node" }
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return loadBootstrapListFixture(t), nil
	}
	listenBootstrap = func(int) (net.Listener, error) {
		t.Fatal("listenBootstrap() called for non-bootstrap node")
		return nil, nil
	}

	if err := startBootstrapService(context.Background()); err != nil {
		t.Fatalf("startBootstrapService() error = %v", err)
	}
}

func TestStartBootstrapServiceReturnsListenError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testBootstrapNodeID }
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return loadBootstrapListFixture(t), nil
	}
	wantErr := errors.New("listen failed")
	listenBootstrap = func(int) (net.Listener, error) {
		return nil, wantErr
	}

	if err := startBootstrapService(context.Background()); !errors.Is(err, wantErr) {
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
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
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
		t.Fatalf(testEncodeStartRequestErrorFormat, err)
	}

	var startResponse bootstrapSessionStartResponse
	if err := json.NewDecoder(clientConn).Decode(&startResponse); err != nil {
		t.Fatalf(testDecodeStartResponseErrorFormat, err)
	}
	if startResponse.Error != "" {
		t.Fatalf(testEmptyStartResponseErrorFormat, startResponse.Error)
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
		t.Fatalf(testEncodeFinalizeRequestErrorFormat, err)
	}

	var finalizeResponse bootstrapSessionFinalizeResponse
	if err := json.NewDecoder(clientConn).Decode(&finalizeResponse); err != nil {
		t.Fatalf(testDecodeFinalizeResponseErrorFormat, err)
	}
	if finalizeResponse.Error != "" {
		t.Fatalf("finalizeResponse.Error = %q, want empty", finalizeResponse.Error)
	}
	if _, ok := writtenFiles[filepath.Join("network", "accounts", accountID+pubkeyFileSuffix)]; !ok {
		t.Fatal("handleBootstrapConnection() did not write the account pubkey")
	}
}

func TestHandleBootstrapConnectionReturnsOnInvalidStartRequest(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		handleBootstrapConnection(serverConn)
		close(done)
	}()

	if _, err := clientConn.Write([]byte(testInvalidJSON)); err != nil {
		t.Fatalf(testWriteErrorFormat, err)
	}
	_ = clientConn.Close()
	<-done
}

func TestHandleBootstrapConnectionReturnsOnStartResponseEncodeError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	conn := &stubConn{
		readData:   mustMarshalJSON(t, bootstrapSessionStartRequest{Type: "start", NodeID: testJoiningNodeID, ListenPort: 58103}),
		writeErr:   errors.New(testWriteFailedText),
		remoteAddr: &net.TCPAddr{IP: net.ParseIP(testBootstrapHost), Port: 43001},
	}
	handleBootstrapConnection(conn)
}

func TestHandleBootstrapConnectionReturnsOnStartResponseError(t *testing.T) {
	conn := &stubConn{
		readData:   mustMarshalJSON(t, bootstrapSessionStartRequest{Type: "start", NodeID: testJoiningNodeID, ListenPort: 58103}),
		remoteAddr: nil,
	}
	handleBootstrapConnection(conn)
	if conn.writeBuffer.Len() == 0 {
		t.Fatal("handleBootstrapConnection() did not encode error start response")
	}
}

func TestHandleBootstrapConnectionReturnsOnFinalizeDecodeError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	conn := &stubConn{
		readData: append(
			mustMarshalJSON(t, bootstrapSessionStartRequest{Type: "start", NodeID: testJoiningNodeID, ListenPort: 58103}),
			[]byte("\n"+testInvalidJSON)...,
		),
		remoteAddr: &net.TCPAddr{IP: net.ParseIP(testBootstrapHost), Port: 43001},
	}
	handleBootstrapConnection(conn)
}

func TestHandleBootstrapConnectionReturnsValidationError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		handleBootstrapConnection(connWithRemoteAddr{
			Conn:       serverConn,
			remoteAddr: &net.TCPAddr{IP: net.ParseIP(testBootstrapHost), Port: 43001},
		})
		close(done)
	}()

	if err := json.NewEncoder(clientConn).Encode(bootstrapSessionStartRequest{
		Type:       "start",
		NodeID:     testJoiningNodeID,
		ListenPort: 58103,
	}); err != nil {
		t.Fatalf(testEncodeStartRequestErrorFormat, err)
	}

	var startResponse bootstrapSessionStartResponse
	if err := json.NewDecoder(clientConn).Decode(&startResponse); err != nil {
		t.Fatalf(testDecodeStartResponseErrorFormat, err)
	}
	if startResponse.Error != "" {
		t.Fatalf(testEmptyStartResponseErrorFormat, startResponse.Error)
	}

	if err := json.NewEncoder(clientConn).Encode(bootstrapSessionFinalizeRequest{
		Type:      "finalize",
		NodeID:    testJoiningNodeID,
		AccountID: testAccountID,
	}); err != nil {
		t.Fatalf(testEncodeFinalizeRequestErrorFormat, err)
	}

	var finalizeResponse bootstrapSessionFinalizeResponse
	if err := json.NewDecoder(clientConn).Decode(&finalizeResponse); err != nil {
		t.Fatalf(testDecodeFinalizeResponseErrorFormat, err)
	}
	if finalizeResponse.Error == "" {
		t.Fatal("handleBootstrapConnection() did not encode validation error response")
	}

	_ = clientConn.Close()
	<-done
}

func TestHandleBootstrapConnectionReturnsWriteNetworkFilesError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, privateKey, blobData, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	readManagedFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	writeManagedFile = func(string, []byte, os.FileMode) error {
		return errors.New(testWriteFailedText)
	}

	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	done := make(chan struct{})
	go func() {
		handleBootstrapConnection(connWithRemoteAddr{
			Conn:       serverConn,
			remoteAddr: &net.TCPAddr{IP: net.ParseIP(testBootstrapHost), Port: 43001},
		})
		close(done)
	}()

	if err := json.NewEncoder(clientConn).Encode(bootstrapSessionStartRequest{
		Type:       "start",
		NodeID:     testJoiningNodeID,
		ListenPort: 58103,
	}); err != nil {
		t.Fatalf(testEncodeStartRequestErrorFormat, err)
	}

	var startResponse bootstrapSessionStartResponse
	if err := json.NewDecoder(clientConn).Decode(&startResponse); err != nil {
		t.Fatalf(testDecodeStartResponseErrorFormat, err)
	}
	if startResponse.Error != "" {
		t.Fatalf(testEmptyStartResponseErrorFormat, startResponse.Error)
	}

	peerData := buildSignedPeerFixture(t, privateKey, testBootstrapHost, 58103, accountID)
	metaData := buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 1)
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
		t.Fatalf(testEncodeFinalizeRequestErrorFormat, err)
	}

	var finalizeResponse bootstrapSessionFinalizeResponse
	if err := json.NewDecoder(clientConn).Decode(&finalizeResponse); err != nil {
		t.Fatalf(testDecodeFinalizeResponseErrorFormat, err)
	}
	if finalizeResponse.Error != testWriteFailedText {
		t.Fatalf("finalizeResponse.Error = %q, want %q", finalizeResponse.Error, testWriteFailedText)
	}

	_ = clientConn.Close()
	<-done
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
		t.Fatalf(testGenerateKeyErrorFormat, err)
	}
	accountID := accounts.AccountIDFromPublicKey(publicKey)
	recoveryBlobData, err := accounts.BuildBlob(accountID, publicKey, privateKey, testRecoveryPassword)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	accountMetaData, err := accounts.BuildMeta(accountID, publicKey, testBootstrapTimestamp, 1, privateKey)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}

	recoverCalled := false
	recoverAccount = func(blobData []byte, password string) (accounts.Material, error) {
		recoverCalled = true
		if string(blobData) != string(recoveryBlobData) {
			t.Fatalf("recoverAccount() blobData = %s, want recovery bundle blob", string(blobData))
		}
		if password != testRecoveryPassword {
			t.Fatalf("recoverAccount() password = %q, want %q", password, testRecoveryPassword)
		}
		return accounts.Material{
			AccountID:    accountID,
			LocalKeyPath: filepath.Join("local", "account", accountID+".key"),
			PublicKey:    publicKey,
			PrivateKey:   privateKey,
			BlobData:     recoveryBlobData,
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
			AccountID:         accountID,
			AccountBlob:       recoveryBlobData,
			RecoveryBundle: recoveryBundleFixture(
				testJoiningNodeID,
				accountID,
				buildSignedPeerFixture(t, privateKey, testBootstrapHost, 58103, accountID),
				buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 9),
				recoveryBlobData,
				accounts.BuildPublicKeyFile(publicKey),
				accountMetaData,
			),
			FirstSeen: testBootstrapTimestamp,
			Revision:  9,
		},
	}
	storePendingSession(session)
	t.Cleanup(func() {
		removePendingSession(testRecoverySessionID)
	})

	writtenFiles := map[string][]byte{}
	writeManagedFile = func(path string, data []byte, perm os.FileMode) error {
		writtenFiles[path] = append([]byte(nil), data...)
		return nil
	}

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
	if string(writtenFiles[testPeerPath()]) == "" {
		t.Fatal("Complete() did not restore peer data")
	}
	if string(writtenFiles[testMetaPath()]) == "" {
		t.Fatal("Complete() did not restore meta data")
	}
}

func TestRestoreRecoveryCompletionHandlesErrorsAndSuccess(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	fixture := buildRecoveryFixture(t)

	t.Run("missing bundle", func(t *testing.T) {
		session := &pendingSession{id: testRecoverySessionID, nodeID: testJoiningNodeID}
		if _, err := restoreRecoveryCompletion(testRecoverySessionID, session, testRecoveryPassword); err == nil || err.Error() != "recovery bundle is missing" {
			t.Fatalf("restoreRecoveryCompletion() error = %v, want missing bundle", err)
		}
	})

	t.Run("missing account id", func(t *testing.T) {
		recoverAccount = func([]byte, string) (accounts.Material, error) {
			return fixture.material, nil
		}
		session := &pendingSession{
			id:     testRecoverySessionID,
			nodeID: testJoiningNodeID,
			response: bootstrapSessionStartResponse{
				RecoveryAvailable: true,
				RecoveryBundle:    fixture.bundle,
			},
		}
		if _, err := restoreRecoveryCompletion(testRecoverySessionID, session, testRecoveryPassword); err == nil || err.Error() != "recovery bundle did not include an account id" {
			t.Fatalf("restoreRecoveryCompletion() error = %v, want missing account id", err)
		}
	})

	t.Run("missing blob", func(t *testing.T) {
		recoverAccount = func([]byte, string) (accounts.Material, error) {
			return fixture.material, nil
		}
		session := &pendingSession{
			id:     testRecoverySessionID,
			nodeID: testJoiningNodeID,
			response: bootstrapSessionStartResponse{
				RecoveryAvailable: true,
				AccountID:         fixture.accountID,
				RecoveryBundle:    fixture.bundle[:4],
			},
		}
		if _, err := restoreRecoveryCompletion(testRecoverySessionID, session, testRecoveryPassword); err == nil || err.Error() != "recovery bundle is missing account blob data" {
			t.Fatalf("restoreRecoveryCompletion() error = %v, want missing blob data", err)
		}
	})

	t.Run("recover error", func(t *testing.T) {
		wantErr := errors.New("recover failed")
		recoverAccount = func([]byte, string) (accounts.Material, error) {
			return accounts.Material{}, wantErr
		}
		session := &pendingSession{
			id:     testRecoverySessionID,
			nodeID: testJoiningNodeID,
			response: bootstrapSessionStartResponse{
				RecoveryAvailable: true,
				AccountID:         fixture.accountID,
				RecoveryBundle:    fixture.bundle,
			},
		}
		if _, err := restoreRecoveryCompletion(testRecoverySessionID, session, testRecoveryPassword); !errors.Is(err, wantErr) {
			t.Fatalf("restoreRecoveryCompletion() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("account mismatch", func(t *testing.T) {
		recoverAccount = func([]byte, string) (accounts.Material, error) {
			material := fixture.material
			material.AccountID = testOtherAccountID
			return material, nil
		}
		session := &pendingSession{
			id:     testRecoverySessionID,
			nodeID: testJoiningNodeID,
			response: bootstrapSessionStartResponse{
				RecoveryAvailable: true,
				AccountID:         fixture.accountID,
				RecoveryBundle:    fixture.bundle,
			},
		}
		if _, err := restoreRecoveryCompletion(testRecoverySessionID, session, testRecoveryPassword); err == nil || err.Error() != "recovery bundle account id does not match recovered account" {
			t.Fatalf("restoreRecoveryCompletion() error = %v, want account mismatch", err)
		}
	})

	t.Run("invalid bundle", func(t *testing.T) {
		recoverAccount = func([]byte, string) (accounts.Material, error) {
			return fixture.material, nil
		}
		badBundle := recoveryBundleFixture(
			testJoiningNodeID,
			fixture.accountID,
			[]byte(testInvalidJSON),
			fixture.metaData,
			fixture.blobData,
			fixture.accountPubKeyData,
			fixture.accountMetaData,
		)
		session := &pendingSession{
			id:     testRecoverySessionID,
			nodeID: testJoiningNodeID,
			response: bootstrapSessionStartResponse{
				RecoveryAvailable: true,
				AccountID:         fixture.accountID,
				RecoveryBundle:    badBundle,
			},
		}
		if _, err := restoreRecoveryCompletion(testRecoverySessionID, session, testRecoveryPassword); err == nil {
			t.Fatal("restoreRecoveryCompletion() error = nil, want bundle validation failure")
		}
	})

	t.Run("write error", func(t *testing.T) {
		recoverAccount = func([]byte, string) (accounts.Material, error) {
			return fixture.material, nil
		}
		wantErr := errors.New(testWriteFailedText)
		writeManagedFile = func(string, []byte, os.FileMode) error { return wantErr }
		session := &pendingSession{
			id:     testRecoverySessionID,
			nodeID: testJoiningNodeID,
			conn:   &stubConn{},
			response: bootstrapSessionStartResponse{
				RecoveryAvailable: true,
				AccountID:         fixture.accountID,
				RecoveryBundle:    fixture.bundle,
			},
		}
		storePendingSession(session)
		t.Cleanup(func() { removePendingSession(testRecoverySessionID) })
		if _, err := restoreRecoveryCompletion(testRecoverySessionID, session, testRecoveryPassword); !errors.Is(err, wantErr) {
			t.Fatalf("restoreRecoveryCompletion() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("load username index error", func(t *testing.T) {
		recoverAccount = func([]byte, string) (accounts.Material, error) {
			return fixture.material, nil
		}
		writeManagedFile = func(string, []byte, os.FileMode) error { return nil }
		wantErr := errors.New("load username index failed")
		loadUsernameIndex = func() (usernameIndex, error) { return nil, wantErr }
		session := &pendingSession{
			id:     testRecoverySessionID,
			nodeID: testJoiningNodeID,
			conn:   &stubConn{},
			response: bootstrapSessionStartResponse{
				RecoveryAvailable: true,
				AccountID:         fixture.accountID,
				UsernameHash:      testAliceHashKey,
				RecoveryBundle:    fixture.bundle,
			},
		}
		storePendingSession(session)
		t.Cleanup(func() { removePendingSession(testRecoverySessionID) })
		if _, err := restoreRecoveryCompletion(testRecoverySessionID, session, testRecoveryPassword); !errors.Is(err, wantErr) {
			t.Fatalf("restoreRecoveryCompletion() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("save username index error", func(t *testing.T) {
		recoverAccount = func([]byte, string) (accounts.Material, error) {
			return fixture.material, nil
		}
		writeManagedFile = func(string, []byte, os.FileMode) error { return nil }
		loadUsernameIndex = func() (usernameIndex, error) { return usernameIndex{}, nil }
		wantErr := errors.New("save username index failed")
		saveUsernameIndex = func(usernameIndex) error { return wantErr }
		session := &pendingSession{
			id:     testRecoverySessionID,
			nodeID: testJoiningNodeID,
			conn:   &stubConn{},
			response: bootstrapSessionStartResponse{
				RecoveryAvailable: true,
				AccountID:         fixture.accountID,
				UsernameHash:      testAliceHashKey,
				RecoveryBundle:    fixture.bundle,
			},
		}
		storePendingSession(session)
		t.Cleanup(func() { removePendingSession(testRecoverySessionID) })
		if _, err := restoreRecoveryCompletion(testRecoverySessionID, session, testRecoveryPassword); !errors.Is(err, wantErr) {
			t.Fatalf("restoreRecoveryCompletion() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("success updates username index", func(t *testing.T) {
		recoverAccount = func([]byte, string) (accounts.Material, error) {
			return fixture.material, nil
		}
		writeManagedFile = func(string, []byte, os.FileMode) error { return nil }
		loadUsernameIndex = func() (usernameIndex, error) { return usernameIndex{}, nil }
		saved := usernameIndex{}
		saveUsernameIndex = func(index usernameIndex) error {
			for key, value := range index {
				saved[key] = append([]string(nil), value...)
			}
			return nil
		}
		session := &pendingSession{
			id:     testRecoverySessionID,
			nodeID: testJoiningNodeID,
			conn:   &stubConn{},
			response: bootstrapSessionStartResponse{
				ObservedIPv4:      testBootstrapHost,
				Port:              58103,
				RecoveryAvailable: true,
				AccountID:         fixture.accountID,
				UsernameHash:      testAliceHashKey,
				RecoveryBundle:    fixture.bundle,
			},
		}
		storePendingSession(session)
		result, err := restoreRecoveryCompletion(testRecoverySessionID, session, testRecoveryPassword)
		if err != nil {
			t.Fatalf("restoreRecoveryCompletion() error = %v", err)
		}
		if got := saved.accountIDsForHash(testAliceHashKey); len(got) != 1 || got[0] != fixture.accountID {
			t.Fatalf("saved username index = %#v, want [%q]", got, fixture.accountID)
		}
		if !strings.Contains(result.Message, "Recovered account") {
			t.Fatalf("restoreRecoveryCompletion() message = %q, want recovery confirmation", result.Message)
		}
	})
}

func TestCompleteReturnsValidationError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	if _, err := Complete("", testAccountPassword); err == nil {
		t.Fatal("Complete() error = nil, want validation failure")
	}
}

func TestCompleteReturnsMissingSessionError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	if _, err := Complete(testSessionID, testAccountPassword); err == nil {
		t.Fatal("Complete() error = nil, want missing session failure")
	}
}

func TestCompleteReturnsCompletionMaterialError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	session := &pendingSession{
		id:     testSessionID,
		conn:   &stubConn{},
		nodeID: testJoiningNodeID,
	}
	storePendingSession(session)
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})

	wantErr := errors.New("create failed")
	createAccount = func(string) (accounts.Material, error) {
		return accounts.Material{}, wantErr
	}

	if _, err := Complete(testSessionID, testAccountPassword); !errors.Is(err, wantErr) {
		t.Fatalf("Complete() error = %v, want %v", err, wantErr)
	}
}

func TestCompleteReturnsBuildArtifactsError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	createAccount = func(string) (accounts.Material, error) {
		return material, nil
	}
	signPeerOrMeta = func(ed25519.PrivateKey, any) (string, error) {
		return "", errors.New(testSignFailedText)
	}

	session := &pendingSession{
		id:     testSessionID,
		conn:   &stubConn{},
		nodeID: testJoiningNodeID,
		response: bootstrapSessionStartResponse{
			ObservedIPv4: testBootstrapHost,
			Port:         58103,
		},
	}
	storePendingSession(session)
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})

	if _, err := Complete(testSessionID, testAccountPassword); err == nil || err.Error() != testSignFailedText {
		t.Fatalf(testCompleteWantErrorFormat, err, testSignFailedText)
	}
}

func TestCompleteReturnsWriteNetworkFilesError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	createAccount = func(string) (accounts.Material, error) {
		return material, nil
	}
	writeManagedFile = func(string, []byte, os.FileMode) error {
		return errors.New(testWriteFailedText)
	}

	session := &pendingSession{
		id:     testSessionID,
		conn:   &stubConn{},
		nodeID: testJoiningNodeID,
		response: bootstrapSessionStartResponse{
			ObservedIPv4: testBootstrapHost,
			Port:         58103,
		},
	}
	storePendingSession(session)
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})

	if _, err := Complete(testSessionID, testAccountPassword); err == nil || err.Error() != testWriteFailedText {
		t.Fatalf(testCompleteWantErrorFormat, err, testWriteFailedText)
	}
}

func TestCompleteReturnsFinalizeError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	createAccount = func(string) (accounts.Material, error) {
		return material, nil
	}
	writeManagedFile = func(string, []byte, os.FileMode) error { return nil }

	session := &pendingSession{
		id:     testSessionID,
		conn:   &stubConn{readData: mustMarshalJSON(t, bootstrapSessionFinalizeResponse{Error: testFinalizeFailedText})},
		nodeID: testJoiningNodeID,
		response: bootstrapSessionStartResponse{
			ObservedIPv4: testBootstrapHost,
			Port:         58103,
		},
	}
	storePendingSession(session)
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})

	if _, err := Complete(testSessionID, testAccountPassword); err == nil || err.Error() != testFinalizeFailedText {
		t.Fatalf(testCompleteWantErrorFormat, err, testFinalizeFailedText)
	}
}

func TestRegisterRejectsDuplicateUsername(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	session := &pendingSession{id: testSessionID, conn: &stubConn{}, nodeID: testJoiningNodeID}
	storePendingSession(session)
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})

	loadUsernameIndex = func() (usernameIndex, error) {
		return usernameIndex{accounts.UsernameHash("alice"): {"account-existing"}}, nil
	}
	createAccount = func(string) (accounts.Material, error) {
		return accounts.Material{AccountID: "account-new"}, nil
	}

	if _, err := Register(testSessionID, "alice", testAccountPassword); err == nil {
		t.Fatal("Register() error = nil, want duplicate username error")
	}
}

func TestRegisterSavesUsernameIndexAfterFinalize(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	createAccount = func(string) (accounts.Material, error) { return material, nil }
	loadUsernameIndex = func() (usernameIndex, error) {
		return usernameIndex{}, nil
	}
	saved := usernameIndex{}
	saveUsernameIndex = func(index usernameIndex) error {
		for key, value := range index {
			saved[key] = append([]string(nil), value...)
		}
		return nil
	}
	saveLocalProfile = func(accountID, username string) (string, error) {
		if accountID != material.AccountID || username != "alice" {
			t.Fatalf(testSaveLocalProfileArgsFormat, accountID, username, material.AccountID, "alice")
		}
		return filepath.Join("local", "account", material.AccountID+".profile"), nil
	}
	writeManagedFile = func(string, []byte, os.FileMode) error { return nil }

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	storePendingSession(&pendingSession{
		id:     testSessionID,
		conn:   clientConn,
		nodeID: testJoiningNodeID,
		response: bootstrapSessionStartResponse{
			ObservedIPv4: testBootstrapHost,
			Port:         58103,
			Reachable:    true,
		},
	})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	go respondToFinalizeRequest(t, serverConn)

	result, err := Register(testSessionID, "alice", testAccountPassword)
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	savedAccounts := saved.accountIDsForHash(accounts.UsernameHash("alice"))
	if len(savedAccounts) != 1 || savedAccounts[0] != material.AccountID {
		t.Fatalf("saved username index account ids = %#v, want [%q]", savedAccounts, material.AccountID)
	}
	if !strings.Contains(result.Message, "Registered alice") {
		t.Fatalf("Register() message = %q, want username confirmation", result.Message)
	}
}

func TestLoginUsesUsernameIndexAndBlob(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	loadUsernameIndex = func() (usernameIndex, error) {
		return usernameIndex{accounts.UsernameHash("alice"): {material.AccountID}}, nil
	}
	recoverAccount = func(blobData []byte, password string) (accounts.Material, error) {
		if string(blobData) != `{"blob":"data"}` || password != testAccountPassword {
			t.Fatalf("recoverAccount() args mismatch")
		}
		return material, nil
	}
	readManagedFile = func(path string) ([]byte, error) {
		if path == accountBlobRelativePath(material.AccountID) {
			return []byte(`{"blob":"data"}`), nil
		}
		return nil, os.ErrNotExist
	}
	saveUsernameIndex = func(index usernameIndex) error {
		accountIDs := index.accountIDsForHash(accounts.UsernameHash("alice"))
		if len(accountIDs) != 1 || accountIDs[0] != material.AccountID {
			t.Fatalf("saveUsernameIndex() account ids = %#v, want [%q]", accountIDs, material.AccountID)
		}
		return nil
	}
	saveLocalProfile = func(accountID, username string) (string, error) {
		if accountID != material.AccountID || username != "alice" {
			t.Fatalf(testSaveLocalProfileArgsFormat, accountID, username, material.AccountID, "alice")
		}
		return filepath.Join("local", "account", material.AccountID+".profile"), nil
	}
	writeManagedFile = func(string, []byte, os.FileMode) error { return nil }

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	storePendingSession(&pendingSession{
		id:       testSessionID,
		conn:     clientConn,
		nodeID:   testJoiningNodeID,
		response: bootstrapSessionStartResponse{ObservedIPv4: testBootstrapHost, Port: 58103},
	})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	go respondToFinalizeRequest(t, serverConn)

	if _, err := Login(testSessionID, "alice", testAccountPassword); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
}

func TestNormalizeUsername(t *testing.T) {
	if _, err := normalizeUsername(" "); err == nil {
		t.Fatal("normalizeUsername() error = nil, want required failure")
	}
	username, err := normalizeUsername("  Alice ")
	if err != nil {
		t.Fatalf("normalizeUsername() error = %v", err)
	}
	if username != "alice" {
		t.Fatalf("normalizeUsername() = %q, want %q", username, "alice")
	}
}

func TestRecoverUsesCompletionPath(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	createAccount = func(password string) (accounts.Material, error) {
		if password != testAccountPassword {
			t.Fatalf("createAccount() password = %q, want %q", password, testAccountPassword)
		}
		return material, nil
	}
	writeManagedFile = func(string, []byte, os.FileMode) error { return nil }

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	storePendingSession(&pendingSession{
		id:       testSessionID,
		conn:     clientConn,
		nodeID:   testJoiningNodeID,
		response: bootstrapSessionStartResponse{ObservedIPv4: testBootstrapHost, Port: 58103},
	})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	go respondToFinalizeRequest(t, serverConn)

	if _, err := Recover(testSessionID, testAccountPassword); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
}

func TestRecoveryBundleMapHandlesErrorsAndSuccess(t *testing.T) {
	if _, err := recoveryBundleMap(nil); err == nil || err.Error() != "recovery bundle is missing" {
		t.Fatalf("recoveryBundleMap(nil) error = %v, want missing bundle", err)
	}
	if _, err := recoveryBundleMap([]recoveryBundleFile{{Path: " ", Data: []byte("x")}}); err == nil || err.Error() != "recovery bundle contains an empty path" {
		t.Fatalf("recoveryBundleMap(blank path) error = %v, want blank path failure", err)
	}
	if _, err := recoveryBundleMap([]recoveryBundleFile{{Path: "path", Data: nil}}); err == nil || !strings.Contains(err.Error(), "recovery bundle file path is empty") {
		t.Fatalf("recoveryBundleMap(empty data) error = %v, want empty file failure", err)
	}

	filesByPath, err := recoveryBundleMap([]recoveryBundleFile{{Path: "path", Data: []byte("data")}})
	if err != nil {
		t.Fatalf("recoveryBundleMap() error = %v", err)
	}
	if string(filesByPath["path"]) != "data" {
		t.Fatalf("recoveryBundleMap()[path] = %q, want %q", string(filesByPath["path"]), "data")
	}
}

func TestBuildRecoveryBundleHandlesMissingStateAndSuccess(t *testing.T) {
	if got := buildRecoveryBundle("", existingNodeRecords{}); got != nil {
		t.Fatalf("buildRecoveryBundle(blank node) = %#v, want nil", got)
	}
	if got := buildRecoveryBundle(testJoiningNodeID, existingNodeRecords{
		NodeMeta: metaFile{AccountID: testAccountID},
	}); got != nil {
		t.Fatalf("buildRecoveryBundle(incomplete) = %#v, want nil", got)
	}

	fixture := buildRecoveryFixture(t)
	got := buildRecoveryBundle(testJoiningNodeID, existingNodeRecords{
		PeerData:          fixture.peerData,
		NodeMetaData:      fixture.metaData,
		AccountPubKeyData: fixture.accountPubKeyData,
		AccountMetaData:   fixture.accountMetaData,
		AccountBlob:       fixture.blobData,
		NodeMeta:          metaFile{AccountID: fixture.accountID},
	})
	if len(got) != 5 {
		t.Fatalf("len(buildRecoveryBundle()) = %d, want %d", len(got), 5)
	}
}

func TestValidateRecoveryBundleFilesHandlesErrorsAndSuccess(t *testing.T) {
	fixture := buildRecoveryFixture(t)

	cases := []struct {
		name  string
		files map[string][]byte
		key   ed25519.PublicKey
	}{
		{name: "missing peer", files: withoutRecoveryFile(fixture.filesByPath, testPeerPath()), key: fixture.publicKey},
		{name: "missing meta", files: withoutRecoveryFile(fixture.filesByPath, testMetaPath()), key: fixture.publicKey},
		{name: "missing pubkey", files: withoutRecoveryFile(fixture.filesByPath, accountPubKeyRelativePath(fixture.accountID)), key: fixture.publicKey},
		{name: "missing account meta", files: withoutRecoveryFile(fixture.filesByPath, accountMetaRelativePath(fixture.accountID)), key: fixture.publicKey},
		{name: "missing blob", files: withoutRecoveryFile(fixture.filesByPath, accountBlobRelativePath(fixture.accountID)), key: fixture.publicKey},
		{name: "pubkey mismatch", files: fixture.filesByPath, key: buildRecoveryFixture(t).publicKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, _, err := validateRecoveryBundleFiles(testJoiningNodeID, fixture.accountID, tc.key, tc.files); err == nil {
				t.Fatal("validateRecoveryBundleFiles() error = nil, want failure")
			}
		})
	}

	t.Run("invalid account meta", func(t *testing.T) {
		files := cloneRecoveryFiles(fixture.filesByPath)
		files[accountMetaRelativePath(fixture.accountID)] = []byte(testInvalidJSON)
		if _, _, _, _, err := validateRecoveryBundleFiles(testJoiningNodeID, fixture.accountID, fixture.publicKey, files); err == nil {
			t.Fatal("validateRecoveryBundleFiles() error = nil, want account meta failure")
		}
	})

	t.Run("invalid blob", func(t *testing.T) {
		files := cloneRecoveryFiles(fixture.filesByPath)
		files[accountBlobRelativePath(fixture.accountID)] = []byte(testInvalidJSON)
		if _, _, _, _, err := validateRecoveryBundleFiles(testJoiningNodeID, fixture.accountID, fixture.publicKey, files); err == nil {
			t.Fatal("validateRecoveryBundleFiles() error = nil, want blob failure")
		}
	})

	t.Run("invalid pubkey", func(t *testing.T) {
		files := cloneRecoveryFiles(fixture.filesByPath)
		files[accountPubKeyRelativePath(fixture.accountID)] = []byte(testInvalidBase64)
		if _, _, _, _, err := validateRecoveryBundleFiles(testJoiningNodeID, fixture.accountID, fixture.publicKey, files); err == nil {
			t.Fatal("validateRecoveryBundleFiles() error = nil, want pubkey failure")
		}
	})

	t.Run("blob pubkey mismatch", func(t *testing.T) {
		other := buildRecoveryFixture(t)
		files := cloneRecoveryFiles(fixture.filesByPath)
		files[accountBlobRelativePath(fixture.accountID)] = other.blobData
		if _, _, _, _, err := validateRecoveryBundleFiles(testJoiningNodeID, fixture.accountID, fixture.publicKey, files); err == nil {
			t.Fatal("validateRecoveryBundleFiles() error = nil, want blob pubkey mismatch")
		}
	})

	t.Run("invalid peer", func(t *testing.T) {
		files := cloneRecoveryFiles(fixture.filesByPath)
		files[testPeerPath()] = []byte(testInvalidJSON)
		if _, _, _, _, err := validateRecoveryBundleFiles(testJoiningNodeID, fixture.accountID, fixture.publicKey, files); err == nil {
			t.Fatal("validateRecoveryBundleFiles() error = nil, want peer failure")
		}
	})

	t.Run("invalid meta", func(t *testing.T) {
		files := cloneRecoveryFiles(fixture.filesByPath)
		files[testMetaPath()] = []byte(testInvalidJSON)
		if _, _, _, _, err := validateRecoveryBundleFiles(testJoiningNodeID, fixture.accountID, fixture.publicKey, files); err == nil {
			t.Fatal("validateRecoveryBundleFiles() error = nil, want meta failure")
		}
	})

	t.Run("success", func(t *testing.T) {
		peerData, metaData, accountPubKeyData, accountMetaData, err := validateRecoveryBundleFiles(testJoiningNodeID, fixture.accountID, fixture.publicKey, fixture.filesByPath)
		if err != nil {
			t.Fatalf("validateRecoveryBundleFiles() error = %v", err)
		}
		if string(peerData) != string(fixture.peerData) || string(metaData) != string(fixture.metaData) || string(accountPubKeyData) != string(fixture.accountPubKeyData) || string(accountMetaData) != string(fixture.accountMetaData) {
			t.Fatal("validateRecoveryBundleFiles() returned mismatched file data")
		}
	})
}

func TestRegisterReturnsUsernameRequiredError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	if _, err := Register(testSessionID, " ", testAccountPassword); err == nil {
		t.Fatal("Register() error = nil, want username required failure")
	}
}

func TestRegisterReturnsInputValidationError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	if _, err := Register("", "alice", testAccountPassword); err == nil {
		t.Fatal("Register() error = nil, want input validation failure")
	}
}

func TestRegisterReturnsMissingSessionError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	if _, err := Register(testSessionID, "alice", testAccountPassword); err == nil {
		t.Fatal("Register() error = nil, want missing session failure")
	}
}

func TestRegisterReturnsCreateAccountError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	storePendingSession(&pendingSession{id: testSessionID, conn: &stubConn{}, nodeID: testJoiningNodeID})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	wantErr := errors.New("create failed")
	createAccount = func(string) (accounts.Material, error) { return accounts.Material{}, wantErr }

	if _, err := Register(testSessionID, "alice", testAccountPassword); !errors.Is(err, wantErr) {
		t.Fatalf(testRegisterWantErrorFormat, err, wantErr)
	}
}

func TestRegisterReturnsUsernameIndexLoadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	storePendingSession(&pendingSession{id: testSessionID, conn: &stubConn{}, nodeID: testJoiningNodeID})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	createAccount = func(string) (accounts.Material, error) {
		return accounts.Material{AccountID: testAccountID}, nil
	}
	wantErr := errors.New("load index failed")
	loadUsernameIndex = func() (usernameIndex, error) { return nil, wantErr }

	if _, err := Register(testSessionID, "alice", testAccountPassword); !errors.Is(err, wantErr) {
		t.Fatalf(testRegisterWantErrorFormat, err, wantErr)
	}
}

func TestRegisterReturnsUsernameIndexSaveError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	createAccount = func(string) (accounts.Material, error) { return material, nil }
	loadUsernameIndex = func() (usernameIndex, error) { return usernameIndex{}, nil }
	writeManagedFile = func(string, []byte, os.FileMode) error { return nil }
	wantErr := errors.New("save index failed")
	saveUsernameIndex = func(usernameIndex) error { return wantErr }

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	storePendingSession(&pendingSession{
		id:       testSessionID,
		conn:     clientConn,
		nodeID:   testJoiningNodeID,
		response: bootstrapSessionStartResponse{ObservedIPv4: testBootstrapHost, Port: 58103},
	})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	go respondToFinalizeRequest(t, serverConn)

	if _, err := Register(testSessionID, "alice", testAccountPassword); !errors.Is(err, wantErr) {
		t.Fatalf(testRegisterWantErrorFormat, err, wantErr)
	}
}

func TestRegisterReturnsFinalizeError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	createAccount = func(string) (accounts.Material, error) { return material, nil }
	loadUsernameIndex = func() (usernameIndex, error) { return usernameIndex{}, nil }
	wantErr := errors.New(testWriteFailedText)
	writeManagedFile = func(string, []byte, os.FileMode) error { return wantErr }
	saveUsernameIndex = func(usernameIndex) error {
		t.Fatal("saveUsernameIndex() called during finalize failure")
		return nil
	}
	saveLocalProfile = func(string, string) (string, error) {
		t.Fatal("saveLocalProfile() called during finalize failure")
		return "", nil
	}
	storePendingSession(&pendingSession{
		id:       testSessionID,
		conn:     &stubConn{},
		nodeID:   testJoiningNodeID,
		response: bootstrapSessionStartResponse{ObservedIPv4: testBootstrapHost, Port: 58103},
	})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})

	if _, err := Register(testSessionID, "alice", testAccountPassword); !errors.Is(err, wantErr) {
		t.Fatalf(testRegisterWantErrorFormat, err, wantErr)
	}
}

func TestRegisterReturnsSaveLocalProfileError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	createAccount = func(string) (accounts.Material, error) { return material, nil }
	loadUsernameIndex = func() (usernameIndex, error) { return usernameIndex{}, nil }
	writeManagedFile = func(string, []byte, os.FileMode) error { return nil }
	saveUsernameIndex = func(usernameIndex) error { return nil }
	wantErr := errors.New("save profile failed")
	saveLocalProfile = func(accountID, username string) (string, error) {
		if accountID != material.AccountID || username != "alice" {
			t.Fatalf(testSaveLocalProfileArgsFormat, accountID, username, material.AccountID, "alice")
		}
		return "", wantErr
	}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	storePendingSession(&pendingSession{
		id:       testSessionID,
		conn:     clientConn,
		nodeID:   testJoiningNodeID,
		response: bootstrapSessionStartResponse{ObservedIPv4: testBootstrapHost, Port: 58103},
	})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	go respondToFinalizeRequest(t, serverConn)

	if _, err := Register(testSessionID, "alice", testAccountPassword); !errors.Is(err, wantErr) {
		t.Fatalf(testRegisterWantErrorFormat, err, wantErr)
	}
}

func TestRegisterAllowsExistingUsernameForSameAccountID(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	createAccount = func(string) (accounts.Material, error) { return material, nil }
	loadUsernameIndex = func() (usernameIndex, error) {
		return usernameIndex{accounts.UsernameHash("alice"): {material.AccountID}}, nil
	}
	writeManagedFile = func(string, []byte, os.FileMode) error { return nil }
	saveUsernameIndex = func(usernameIndex) error { return nil }
	saveLocalProfile = func(string, string) (string, error) { return "", nil }

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	storePendingSession(&pendingSession{
		id:       testSessionID,
		conn:     clientConn,
		nodeID:   testJoiningNodeID,
		response: bootstrapSessionStartResponse{ObservedIPv4: testBootstrapHost, Port: 58103},
	})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	go respondToFinalizeRequest(t, serverConn)

	if _, err := Register(testSessionID, "alice", testAccountPassword); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
}

func TestLoginReturnsUsernameRequiredError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	if _, err := Login(testSessionID, " ", testAccountPassword); err == nil {
		t.Fatal("Login() error = nil, want username required failure")
	}
}

func TestLoginReturnsInputValidationError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	if _, err := Login("", "alice", testAccountPassword); err == nil {
		t.Fatal("Login() error = nil, want input validation failure")
	}
}

func TestLoginReturnsMissingSessionError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	if _, err := Login(testSessionID, "alice", testAccountPassword); err == nil || err.Error() != testSessionMissingText {
		t.Fatalf("Login() error = %v, want %q", err, testSessionMissingText)
	}
}

func TestLoginReturnsUsernameIndexLoadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	storePendingSession(&pendingSession{id: testSessionID, conn: &stubConn{}, nodeID: testJoiningNodeID})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	wantErr := errors.New("load index failed")
	loadUsernameIndex = func() (usernameIndex, error) { return nil, wantErr }

	if _, err := Login(testSessionID, "alice", testAccountPassword); !errors.Is(err, wantErr) {
		t.Fatalf(testLoginWantErrorFormat, err, wantErr)
	}
}

func TestLoginReturnsMissingUsernameError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	storePendingSession(&pendingSession{id: testSessionID, conn: &stubConn{}, nodeID: testJoiningNodeID})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	loadUsernameIndex = func() (usernameIndex, error) { return usernameIndex{}, nil }

	if _, err := Login(testSessionID, "alice", testAccountPassword); err == nil {
		t.Fatal("Login() error = nil, want username missing failure")
	}
}

func TestLoginReturnsAmbiguousUsernameError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	storePendingSession(&pendingSession{id: testSessionID, conn: &stubConn{}, nodeID: testJoiningNodeID})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	loadUsernameIndex = func() (usernameIndex, error) {
		return usernameIndex{accounts.UsernameHash("alice"): {testFirstIndexedAccountID, testSecondIndexedAccountID}}, nil
	}

	if _, err := Login(testSessionID, "alice", testAccountPassword); err == nil {
		t.Fatal("Login() error = nil, want ambiguous username failure")
	}
}

func TestLoginReturnsBlobMissingError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	storePendingSession(&pendingSession{id: testSessionID, conn: &stubConn{}, nodeID: testJoiningNodeID})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	loadUsernameIndex = func() (usernameIndex, error) {
		return usernameIndex{accounts.UsernameHash("alice"): {testAccountID}}, nil
	}
	readManagedFile = func(string) ([]byte, error) { return nil, os.ErrNotExist }

	if _, err := Login(testSessionID, "alice", testAccountPassword); err == nil {
		t.Fatal("Login() error = nil, want blob missing failure")
	}
}

func TestLoginReturnsBlobReadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	storePendingSession(&pendingSession{id: testSessionID, conn: &stubConn{}, nodeID: testJoiningNodeID})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	loadUsernameIndex = func() (usernameIndex, error) {
		return usernameIndex{accounts.UsernameHash("alice"): {testAccountID}}, nil
	}
	wantErr := errors.New("read failed")
	readManagedFile = func(string) ([]byte, error) { return nil, wantErr }

	if _, err := Login(testSessionID, "alice", testAccountPassword); !errors.Is(err, wantErr) {
		t.Fatalf(testLoginWantErrorFormat, err, wantErr)
	}
}

func TestLoginReturnsRecoverError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	storePendingSession(&pendingSession{id: testSessionID, conn: &stubConn{}, nodeID: testJoiningNodeID})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	loadUsernameIndex = func() (usernameIndex, error) {
		return usernameIndex{accounts.UsernameHash("alice"): {testAccountID}}, nil
	}
	readManagedFile = func(string) ([]byte, error) { return []byte(`{"blob":"data"}`), nil }
	wantErr := errors.New("recover failed")
	recoverAccount = func([]byte, string) (accounts.Material, error) { return accounts.Material{}, wantErr }

	if _, err := Login(testSessionID, "alice", testAccountPassword); !errors.Is(err, wantErr) {
		t.Fatalf(testLoginWantErrorFormat, err, wantErr)
	}
}

func TestLoginReturnsRecoveredAccountMismatchError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	storePendingSession(&pendingSession{id: testSessionID, conn: &stubConn{}, nodeID: testJoiningNodeID})
	t.Cleanup(func() {
		removePendingSession(testSessionID)
	})
	loadUsernameIndex = func() (usernameIndex, error) {
		return usernameIndex{accounts.UsernameHash("alice"): {testAccountID}}, nil
	}
	readManagedFile = func(string) ([]byte, error) { return []byte(`{"blob":"data"}`), nil }
	recoverAccount = func([]byte, string) (accounts.Material, error) {
		return accounts.Material{AccountID: testOtherAccountID}, nil
	}

	if _, err := Login(testSessionID, "alice", testAccountPassword); err == nil {
		t.Fatal("Login() error = nil, want recovered account mismatch")
	}
}

func TestLoadUsernameIndexCacheHandlesReadPaths(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(path string) ([]byte, error) {
		if path != usernameIndexPath {
			t.Fatalf("readManagedFile() path = %q, want %q", path, usernameIndexPath)
		}
		return nil, os.ErrNotExist
	}
	index, err := loadUsernameIndexCache()
	if err != nil {
		t.Fatalf("loadUsernameIndexCache() error = %v", err)
	}
	if len(index) != 0 {
		t.Fatalf("loadUsernameIndexCache() len = %d, want 0", len(index))
	}

	readManagedFile = func(string) ([]byte, error) {
		return []byte(`{"` + testAliceHashKey + `":["` + testFirstIndexedAccountID + `"]}`), nil
	}
	index, err = loadUsernameIndexCache()
	if err != nil {
		t.Fatalf("loadUsernameIndexCache() success-path error = %v", err)
	}
	if got := index.accountIDsForHash(testAliceHashKey); len(got) != 1 || got[0] != testFirstIndexedAccountID {
		t.Fatalf("loadUsernameIndexCache() index[%s] = %#v, want [%s]", testAliceHashKey, got, testFirstIndexedAccountID)
	}

	readManagedFile = func(string) ([]byte, error) { return nil, errors.New("read failed") }
	if _, err := loadUsernameIndexCache(); err == nil {
		t.Fatal("loadUsernameIndexCache() error = nil, want read failure")
	}

	readManagedFile = func(string) ([]byte, error) { return []byte("{bad"), nil }
	if _, err := loadUsernameIndexCache(); err == nil {
		t.Fatal("loadUsernameIndexCache() error = nil, want unmarshal failure")
	}
}

func TestUsernameIndexAccountIDsForHashHandlesBlankAndDuplicates(t *testing.T) {
	index := usernameIndex{
		testAliceHashKey: {" ", testFirstIndexedAccountID, testFirstIndexedAccountID, testSecondIndexedAccountID, testSecondIndexedAccountID},
	}

	if got := index.accountIDsForHash(" "); got != nil {
		t.Fatalf("accountIDsForHash(blank) = %#v, want nil", got)
	}

	got := index.accountIDsForHash(testAliceHashKey)
	want := []string{testFirstIndexedAccountID, testSecondIndexedAccountID}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("accountIDsForHash(%q) = %#v, want %#v", testAliceHashKey, got, want)
	}
}

func TestUsernameIndexAddSkipsNilAndBlankEntries(t *testing.T) {
	var nilIndex usernameIndex
	nilIndex.add(testAliceHashKey, testAccountID)

	index := usernameIndex{}
	index.add(" ", testAccountID)
	index.add(testAliceHashKey, " ")
	if len(index) != 0 {
		t.Fatalf("username index after blank add = %#v, want empty", index)
	}
}

func TestSaveUsernameIndexCachePersistsJSON(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	calls := 0
	writeManagedFile = func(path string, data []byte, perm os.FileMode) error {
		calls++
		if path != usernameIndexPath {
			t.Fatalf("writeManagedFile() path = %q, want %q", path, usernameIndexPath)
		}
		if perm != 0o644 {
			t.Fatalf("writeManagedFile() perm = %#o, want %#o", perm, 0o644)
		}
		if calls == 1 && !strings.Contains(string(data), `"`+testAliceHashKey+`"`) {
			t.Fatalf("writeManagedFile() data = %s, want username index json", string(data))
		}
		return nil
	}

	if err := saveUsernameIndexCache(usernameIndex{testAliceHashKey: {testAccountID}}); err != nil {
		t.Fatalf("saveUsernameIndexCache() error = %v", err)
	}
	if err := saveUsernameIndexCache(nil); err != nil {
		t.Fatalf("saveUsernameIndexCache(nil) error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("writeManagedFile() calls = %d, want 2", calls)
	}
}

func TestSaveUsernameIndexCacheReturnsWriteError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New("write failed")
	writeManagedFile = func(string, []byte, os.FileMode) error { return wantErr }

	if err := saveUsernameIndexCache(usernameIndex{testAliceHashKey: {testAccountID}}); !errors.Is(err, wantErr) {
		t.Fatalf("saveUsernameIndexCache() error = %v, want %v", err, wantErr)
	}
}

func TestSaveUsernameIndexCacheReturnsMarshalError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New("marshal failed")
	marshalIndexJSON = func(usernameIndex) ([]byte, error) { return nil, wantErr }

	if err := saveUsernameIndexCache(usernameIndex{testAliceHashKey: {testAccountID}}); !errors.Is(err, wantErr) {
		t.Fatalf("saveUsernameIndexCache() error = %v, want %v", err, wantErr)
	}
}

func TestValidateCompletionInputsRejectsMissingSessionID(t *testing.T) {
	if err := validateCompletionInputs("", testAccountPassword); err == nil {
		t.Fatal("validateCompletionInputs() error = nil, want session id failure")
	}
}

func TestValidateCompletionInputsRejectsMissingPassword(t *testing.T) {
	if err := validateCompletionInputs(testSessionID, ""); err == nil {
		t.Fatal("validateCompletionInputs() error = nil, want password failure")
	}
}

func TestPendingSessionForCompletionReturnsMissingSessionError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	if _, err := pendingSessionForCompletion(testSessionID); err == nil || err.Error() != testSessionMissingText {
		t.Fatalf("pendingSessionForCompletion() error = %v, want %q", err, testSessionMissingText)
	}
}

func TestNormalizeCompletionStateDefaultsMissingValues(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	currentTime = func() time.Time {
		return time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)
	}

	state := normalizeCompletionState(bootstrapSessionStartResponse{})
	if state.firstSeen == "" || state.accountCreatedAt == "" {
		t.Fatal("normalizeCompletionState() missing default timestamps")
	}
	if state.revision != 1 || state.accountRevision != 1 {
		t.Fatalf("normalizeCompletionState() revisions = (%d, %d), want (1, 1)", state.revision, state.accountRevision)
	}
}

func TestFinalizeBootstrapSessionReturnsEncodeError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	session := &pendingSession{
		id:     testSessionID,
		conn:   &stubConn{writeErr: errors.New(testWriteFailedText)},
		nodeID: testJoiningNodeID,
	}

	err := finalizeBootstrapSession(testSessionID, session, material, completionArtifacts{})
	if err == nil {
		t.Fatal("finalizeBootstrapSession() error = nil, want encode failure")
	}
}

func TestFinalizeBootstrapSessionReturnsDecodeError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	session := &pendingSession{
		id:     testSessionID,
		conn:   &stubConn{readData: []byte(testInvalidJSON)},
		nodeID: testJoiningNodeID,
	}

	err := finalizeBootstrapSession(testSessionID, session, material, completionArtifacts{})
	if err == nil {
		t.Fatal("finalizeBootstrapSession() error = nil, want decode failure")
	}
}

func TestFinalizeBootstrapSessionReturnsFinalizeResponseError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	session := &pendingSession{
		id:     testSessionID,
		conn:   &stubConn{readData: mustMarshalJSON(t, bootstrapSessionFinalizeResponse{Error: testFinalizeFailedText})},
		nodeID: testJoiningNodeID,
	}

	err := finalizeBootstrapSession(testSessionID, session, material, completionArtifacts{})
	if err == nil || err.Error() != testFinalizeFailedText {
		t.Fatalf("finalizeBootstrapSession() error = %v, want %s", err, testFinalizeFailedText)
	}
}

func TestValidateFinalizeRequestRejectsNodeAccountTakeover(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	originalAccountID, originalPrivateKey, originalBlobData, originalPubKeyData, originalAccountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf("buildAccountFixtures() original error = %v", err)
	}
	newAccountID, newPrivateKey, newBlobData, newPubKeyData, newAccountMetaData, err := buildAccountFixtures(testUpdatedTimestamp)
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

func TestBootstrapPortForNodeUsesDefaultPortWhenConfiguredPortIsInvalid(t *testing.T) {
	port, ok := bootstrapPortForNode(bootstrapList{
		Nodes: map[string]bootstrapNodeConfig{
			testBootstrapName: {
				NodeID: testBootstrapNodeID,
				Port:   0,
			},
		},
	}, testBootstrapNodeID)
	if !ok || port != DefaultPort {
		t.Fatalf("bootstrapPortForNode() = (%d, %t), want (%d, %t)", port, ok, DefaultPort, true)
	}
}

func TestDefaultListenPortReturnsDefaultOnLoadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return nil, errors.New(testLoadNodesErrorText)
	}

	if got := defaultListenPort(testJoiningNodeID); got != DefaultPort {
		t.Fatalf(testDefaultListenPortFormat, got, DefaultPort)
	}
}

func TestDefaultListenPortReturnsDefaultWhenNodeIsUnknown(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return loadBootstrapListFixture(t), nil
	}

	if got := defaultListenPort("missing-node"); got != DefaultPort {
		t.Fatalf(testDefaultListenPortFormat, got, DefaultPort)
	}
}

func TestDefaultListenPortReturnsConfiguredBootstrapPort(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return []byte(fmt.Sprintf(`
version: 1
nodes:
  local:
    node_id: "`+testJoiningNodeID+`"
    host: "%s"
    port: 60001
`, testLoopbackHost)), nil
	}

	if got := defaultListenPort(testJoiningNodeID); got != 60001 {
		t.Fatalf(testDefaultListenPortFormat, got, 60001)
	}
}

func TestBuildBootstrapStartResponseReturnsErrorForInvalidRemoteAddr(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	response := buildBootstrapStartResponse(nil, bootstrapSessionStartRequest{NodeID: testJoiningNodeID})
	if response.Error == "" {
		t.Fatal("buildBootstrapStartResponse() Error = empty, want remote address failure")
	}
}

func TestBuildBootstrapStartResponseReturnsExistingRecordError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(path string) ([]byte, error) {
		if path == testPeerPath() {
			return nil, errors.New(testPeerReadFailedText)
		}
		return nil, os.ErrNotExist
	}

	response := buildBootstrapStartResponse(&net.TCPAddr{IP: net.ParseIP(testBootstrapHost), Port: 43001}, bootstrapSessionStartRequest{
		NodeID:     testJoiningNodeID,
		ListenPort: 58103,
	})
	if response.Error == "" {
		t.Fatal("buildBootstrapStartResponse() Error = empty, want existing record failure")
	}
}

func TestLoadExistingNodeRecordsReturnsZeroForBlankNodeID(t *testing.T) {
	records, err := loadExistingNodeRecords(" ")
	if err != nil {
		t.Fatalf(testLoadExistingNodeRecordsErrorFormat, err)
	}
	if records.KnownNode {
		t.Fatal("loadExistingNodeRecords() KnownNode = true, want false")
	}
}

func TestLoadExistingNodeRecordsReturnsZeroForMissingPeer(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}

	records, err := loadExistingNodeRecords(testJoiningNodeID)
	if err != nil {
		t.Fatalf(testLoadExistingNodeRecordsErrorFormat, err)
	}
	if records.KnownNode {
		t.Fatal("loadExistingNodeRecords() KnownNode = true, want false")
	}
}

func TestLoadExistingNodeRecordsReturnsPeerReadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New(testPeerReadFailedText)
	readManagedFile = func(path string) ([]byte, error) {
		if path == testPeerPath() {
			return nil, wantErr
		}
		return nil, os.ErrNotExist
	}

	if _, err := loadExistingNodeRecords(testJoiningNodeID); !errors.Is(err, wantErr) {
		t.Fatalf("loadExistingNodeRecords() error = %v, want %v", err, wantErr)
	}
}

func TestLoadExistingNodeRecordsReturnsKnownNodeWithoutMeta(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(path string) ([]byte, error) {
		switch path {
		case testPeerPath():
			return []byte(`{}`), nil
		case testMetaPath():
			return nil, os.ErrNotExist
		default:
			return nil, os.ErrNotExist
		}
	}

	records, err := loadExistingNodeRecords(testJoiningNodeID)
	if err != nil {
		t.Fatalf(testLoadExistingNodeRecordsErrorFormat, err)
	}
	if !records.KnownNode {
		t.Fatal("loadExistingNodeRecords() KnownNode = false, want true")
	}
}

func TestLoadExistingNodeRecordsReturnsMetaDecodeError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(path string) ([]byte, error) {
		switch path {
		case testPeerPath():
			return []byte(`{}`), nil
		case testMetaPath():
			return []byte(testInvalidJSON), nil
		default:
			return nil, os.ErrNotExist
		}
	}

	if _, err := loadExistingNodeRecords(testJoiningNodeID); err == nil {
		t.Fatal("loadExistingNodeRecords() error = nil, want meta decode failure")
	}
}

func TestLoadExistingNodeRecordsReturnsKnownNodeWithoutAccount(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(path string) ([]byte, error) {
		switch path {
		case testPeerPath():
			return []byte(`{}`), nil
		case testMetaPath():
			return mustMarshalJSON(t, metaFile{
				NodeID:    testJoiningNodeID,
				AccountID: "",
				FirstSeen: testBootstrapTimestamp,
				Revision:  1,
				UpdatedAt: testBootstrapTimestamp,
			}), nil
		default:
			return nil, os.ErrNotExist
		}
	}

	records, err := loadExistingNodeRecords(testJoiningNodeID)
	if err != nil {
		t.Fatalf(testLoadExistingNodeRecordsErrorFormat, err)
	}
	if !records.KnownNode {
		t.Fatal("loadExistingNodeRecords() KnownNode = false, want true")
	}
}

func TestLoadExistingNodeMetaReturnsReadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New("meta read failed")
	readManagedFile = func(string) ([]byte, error) {
		return nil, wantErr
	}

	if _, _, _, err := loadExistingNodeMeta(testJoiningNodeID); !errors.Is(err, wantErr) {
		t.Fatalf("loadExistingNodeMeta() error = %v, want %v", err, wantErr)
	}
}

func TestLoadExistingAccountPubKeyReturnsMissing(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}

	publicKey, ok, err := loadExistingAccountPubKey(testAccountID)
	if err != nil {
		t.Fatalf("loadExistingAccountPubKey() error = %v", err)
	}
	if ok || publicKey != nil {
		t.Fatalf("loadExistingAccountPubKey() = (%v, %t), want (nil, false)", publicKey, ok)
	}
}

func TestLoadExistingAccountPubKeyReturnsReadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New("pubkey read failed")
	readManagedFile = func(string) ([]byte, error) {
		return nil, wantErr
	}

	if _, _, err := loadExistingAccountPubKey(testAccountID); !errors.Is(err, wantErr) {
		t.Fatalf("loadExistingAccountPubKey() error = %v, want %v", err, wantErr)
	}
}

func TestLoadExistingAccountPubKeyReturnsVerifyError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(string) ([]byte, error) {
		return []byte(testInvalidBase64), nil
	}

	if _, _, err := loadExistingAccountPubKey(testAccountID); err == nil {
		t.Fatal("loadExistingAccountPubKey() error = nil, want verify failure")
	}
}

func TestLoadExistingAccountMetaReturnsMissing(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	_, publicKey, _, _, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	pubKeyData := publicKey.Public().(ed25519.PublicKey)

	meta, ok, err := loadExistingAccountMeta(testAccountID, pubKeyData)
	if err != nil {
		t.Fatalf("loadExistingAccountMeta() error = %v", err)
	}
	if ok || meta != (accounts.Meta{}) {
		t.Fatalf("loadExistingAccountMeta() = (%#v, %t), want zero, false", meta, ok)
	}
}

func TestLoadExistingAccountMetaReturnsReadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New("account meta read failed")
	readManagedFile = func(string) ([]byte, error) {
		return nil, wantErr
	}
	_, publicKey, _, _, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	pubKeyData := publicKey.Public().(ed25519.PublicKey)

	if _, _, err := loadExistingAccountMeta(testAccountID, pubKeyData); !errors.Is(err, wantErr) {
		t.Fatalf("loadExistingAccountMeta() error = %v, want %v", err, wantErr)
	}
}

func TestLoadExistingAccountMetaReturnsVerifyError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(string) ([]byte, error) {
		return []byte(testInvalidJSON), nil
	}
	_, publicKey, _, _, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	pubKeyData := publicKey.Public().(ed25519.PublicKey)

	if _, _, err := loadExistingAccountMeta(testAccountID, pubKeyData); err == nil {
		t.Fatal("loadExistingAccountMeta() error = nil, want verify failure")
	}
}

func TestLoadExistingAccountBlobReturnsMissing(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}
	accountID, _, _, accountPubKeyData, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	accountPubKey, err := accounts.VerifyPublicKeyFile(accountID, accountPubKeyData)
	if err != nil {
		t.Fatalf(testVerifyPublicKeyFileErrorFormat, err)
	}

	data, ok, err := loadExistingAccountBlob(accountID, accountPubKey)
	if err != nil {
		t.Fatalf("loadExistingAccountBlob() error = %v", err)
	}
	if ok || data != nil {
		t.Fatalf("loadExistingAccountBlob() = (%v, %t), want (nil, false)", data, ok)
	}
}

func TestLoadExistingAccountBlobReturnsReadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New("blob read failed")
	readManagedFile = func(string) ([]byte, error) {
		return nil, wantErr
	}
	accountID, _, _, accountPubKeyData, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	accountPubKey, err := accounts.VerifyPublicKeyFile(accountID, accountPubKeyData)
	if err != nil {
		t.Fatalf(testVerifyPublicKeyFileErrorFormat, err)
	}

	if _, _, err := loadExistingAccountBlob(accountID, accountPubKey); !errors.Is(err, wantErr) {
		t.Fatalf("loadExistingAccountBlob() error = %v, want %v", err, wantErr)
	}
}

func TestLoadExistingAccountBlobReturnsValidateError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(string) ([]byte, error) {
		return []byte(testInvalidJSON), nil
	}
	accountID, _, _, accountPubKeyData, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	accountPubKey, err := accounts.VerifyPublicKeyFile(accountID, accountPubKeyData)
	if err != nil {
		t.Fatalf(testVerifyPublicKeyFileErrorFormat, err)
	}

	if _, _, err := loadExistingAccountBlob(accountID, accountPubKey); err == nil {
		t.Fatal("loadExistingAccountBlob() error = nil, want blob validation failure")
	}
}

func TestLoadExistingAccountBlobRejectsPublicKeyMismatch(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, _, blobData, _, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	otherAccountID, _, _, otherPubKeyData, _, err := buildAccountFixtures(testUpdatedTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesOtherErrorFormat, err)
	}
	readManagedFile = func(string) ([]byte, error) {
		return blobData, nil
	}
	accountPubKey, err := accounts.VerifyPublicKeyFile(otherAccountID, otherPubKeyData)
	if err != nil {
		t.Fatalf(testVerifyPublicKeyFileErrorFormat, err)
	}

	if _, _, err := loadExistingAccountBlob(accountID, accountPubKey); err == nil {
		t.Fatal("loadExistingAccountBlob() error = nil, want pubkey mismatch")
	}
}

func TestLoadExistingAccountRecordsReturnsPubKeyError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New("pubkey read failed")
	readManagedFile = func(string) ([]byte, error) {
		return nil, wantErr
	}

	records := existingNodeRecords{
		KnownNode: true,
		NodeMeta: metaFile{
			NodeID:    testJoiningNodeID,
			AccountID: testAccountID,
		},
	}

	if _, err := loadExistingAccountRecords(testJoiningNodeID, []byte("{}"), records); !errors.Is(err, wantErr) {
		t.Fatalf(testLoadExistingAccountRecordsWantErrFmt, err, wantErr)
	}
}

func TestLoadExistingAccountRecordsReturnsWithoutPubKey(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	readManagedFile = func(string) ([]byte, error) {
		return nil, os.ErrNotExist
	}

	records := existingNodeRecords{
		KnownNode: true,
		NodeMeta: metaFile{
			NodeID:    testJoiningNodeID,
			AccountID: testAccountID,
		},
	}

	got, err := loadExistingAccountRecords(testJoiningNodeID, []byte("{}"), records)
	if err != nil {
		t.Fatalf("loadExistingAccountRecords() error = %v", err)
	}
	if len(got.AccountPubKey) != 0 {
		t.Fatal("loadExistingAccountRecords() AccountPubKey populated, want empty")
	}
}

func TestLoadExistingAccountRecordsReturnsRawPubKeyReadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, _, _, accountPubKeyData, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	wantErr := errors.New("raw pubkey read failed")
	readCounts := map[string]int{}
	readManagedFile = func(path string) ([]byte, error) {
		readCounts[path]++
		switch path {
		case accountPubKeyRelativePath(accountID):
			if readCounts[path] == 1 {
				return accountPubKeyData, nil
			}
			return nil, wantErr
		default:
			return nil, os.ErrNotExist
		}
	}

	records := existingNodeRecords{
		KnownNode: true,
		NodeMeta: metaFile{
			NodeID:    testJoiningNodeID,
			AccountID: accountID,
		},
	}

	if _, err := loadExistingAccountRecords(testJoiningNodeID, []byte("{}"), records); !errors.Is(err, wantErr) {
		t.Fatalf(testLoadExistingAccountRecordsWantErrFmt, err, wantErr)
	}
}

func TestLoadExistingAccountRecordsReturnsAccountMetaError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, _, _, accountPubKeyData, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	wantErr := errors.New("account meta read failed")
	readManagedFile = func(path string) ([]byte, error) {
		switch path {
		case accountPubKeyRelativePath(accountID):
			return accountPubKeyData, nil
		case accountMetaRelativePath(accountID):
			return nil, wantErr
		default:
			return nil, os.ErrNotExist
		}
	}

	records := existingNodeRecords{
		KnownNode: true,
		NodeMeta: metaFile{
			NodeID:    testJoiningNodeID,
			AccountID: accountID,
		},
	}

	if _, err := loadExistingAccountRecords(testJoiningNodeID, []byte("{}"), records); !errors.Is(err, wantErr) {
		t.Fatalf(testLoadExistingAccountRecordsWantErrFmt, err, wantErr)
	}
}

func TestLoadExistingAccountRecordsReturnsWithoutAccountMeta(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, _, _, accountPubKeyData, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	readManagedFile = func(path string) ([]byte, error) {
		switch path {
		case accountPubKeyRelativePath(accountID):
			return accountPubKeyData, nil
		case accountMetaRelativePath(accountID):
			return nil, os.ErrNotExist
		default:
			return nil, os.ErrNotExist
		}
	}

	records := existingNodeRecords{
		KnownNode: true,
		NodeMeta: metaFile{
			NodeID:    testJoiningNodeID,
			AccountID: accountID,
		},
	}

	got, err := loadExistingAccountRecords(testJoiningNodeID, []byte("{}"), records)
	if err != nil {
		t.Fatalf("loadExistingAccountRecords() error = %v", err)
	}
	if len(got.AccountPubKey) == 0 {
		t.Fatal("loadExistingAccountRecords() missing trusted account pubkey")
	}
	if got.AccountMeta != (accounts.Meta{}) {
		t.Fatalf("loadExistingAccountRecords() AccountMeta = %#v, want zero value", got.AccountMeta)
	}
}

func TestLoadExistingAccountRecordsReturnsRawAccountMetaReadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, privateKey, _, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	wantErr := errors.New("raw account meta read failed")
	readCounts := map[string]int{}
	nodeMetaData := buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 1)
	readManagedFile = func(path string) ([]byte, error) {
		readCounts[path]++
		switch path {
		case accountPubKeyRelativePath(accountID):
			return accountPubKeyData, nil
		case accountMetaRelativePath(accountID):
			if readCounts[path] == 1 {
				return accountMetaData, nil
			}
			return nil, wantErr
		default:
			return nil, os.ErrNotExist
		}
	}

	records := existingNodeRecords{
		KnownNode: true,
		NodeMeta: metaFile{
			NodeID:    testJoiningNodeID,
			AccountID: accountID,
		},
	}

	if _, err := loadExistingAccountRecords(testJoiningNodeID, nodeMetaData, records); !errors.Is(err, wantErr) {
		t.Fatalf(testLoadExistingAccountRecordsWantErrFmt, err, wantErr)
	}
}

func TestLoadExistingAccountRecordsReturnsVerifyMetaError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, _, _, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	readManagedFile = func(path string) ([]byte, error) {
		switch path {
		case accountPubKeyRelativePath(accountID):
			return accountPubKeyData, nil
		case accountMetaRelativePath(accountID):
			return accountMetaData, nil
		default:
			return nil, os.ErrNotExist
		}
	}

	records := existingNodeRecords{
		KnownNode: true,
		NodeMeta: metaFile{
			NodeID:    testJoiningNodeID,
			AccountID: accountID,
		},
	}

	if _, err := loadExistingAccountRecords(testJoiningNodeID, []byte(testInvalidJSON), records); err == nil {
		t.Fatal("loadExistingAccountRecords() error = nil, want node meta verification failure")
	}
}

func TestLoadExistingAccountRecordsReturnsBlobError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, privateKey, _, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	wantErr := errors.New("blob read failed")
	nodeMetaData := buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 1)
	readManagedFile = func(path string) ([]byte, error) {
		switch path {
		case accountPubKeyRelativePath(accountID):
			return accountPubKeyData, nil
		case accountMetaRelativePath(accountID):
			return accountMetaData, nil
		case accountBlobRelativePath(accountID):
			return nil, wantErr
		default:
			return nil, os.ErrNotExist
		}
	}

	records := existingNodeRecords{
		KnownNode: true,
		NodeMeta: metaFile{
			NodeID:    testJoiningNodeID,
			AccountID: accountID,
		},
	}

	if _, err := loadExistingAccountRecords(testJoiningNodeID, nodeMetaData, records); !errors.Is(err, wantErr) {
		t.Fatalf(testLoadExistingAccountRecordsWantErrFmt, err, wantErr)
	}
}

func TestExtractObservedIPv4ReturnsErrors(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr net.Addr
	}{
		{name: "nil", remoteAddr: nil},
		{name: "bad-host-port", remoteAddr: stubAddr{text: "not-a-host-port"}},
		{name: "ipv6", remoteAddr: &net.TCPAddr{IP: net.ParseIP("::1"), Port: 43001}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := extractObservedIPv4(tc.remoteAddr); err == nil {
				t.Fatal("extractObservedIPv4() error = nil, want failure")
			}
		})
	}
}

func TestSignPayloadReturnsMarshalError(t *testing.T) {
	if _, err := signPayload(make(ed25519.PrivateKey, ed25519.PrivateKeySize), make(chan int)); err == nil {
		t.Fatal("signPayload() error = nil, want marshal failure")
	}
}

func TestValidateFinalizeRequestRejectsInvalidPubKey(t *testing.T) {
	if err := validateFinalizeRequest(bootstrapSessionFinalizeRequest{
		AccountID:     testAccountID,
		AccountPubKey: []byte(testInvalidBase64),
	}); err == nil {
		t.Fatal("validateFinalizeRequest() error = nil, want pubkey failure")
	}
}

func TestValidateFinalizeRequestRejectsInvalidPeerAndMetaPayloads(t *testing.T) {
	accountID, privateKey, blobData, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}

	if err := validateFinalizeRequest(bootstrapSessionFinalizeRequest{
		NodeID:        testJoiningNodeID,
		AccountID:     accountID,
		AccountPubKey: accountPubKeyData,
		AccountMeta:   accountMetaData,
		AccountBlob:   blobData,
		PeerData:      []byte(testInvalidJSON),
		MetaData:      buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 1),
	}); err == nil {
		t.Fatal("validateFinalizeRequest() error = nil, want peer validation failure")
	}

	if err := validateFinalizeRequest(bootstrapSessionFinalizeRequest{
		NodeID:        testJoiningNodeID,
		AccountID:     accountID,
		AccountPubKey: accountPubKeyData,
		AccountMeta:   accountMetaData,
		AccountBlob:   blobData,
		PeerData:      buildSignedPeerFixture(t, privateKey, testBootstrapHost, 58103, accountID),
		MetaData:      []byte(testInvalidJSON),
	}); err == nil {
		t.Fatal("validateFinalizeRequest() error = nil, want meta validation failure")
	}
}

func TestValidateFinalizeRequestRejectsInvalidAccountMeta(t *testing.T) {
	accountID, privateKey, blobData, accountPubKeyData, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}

	if err := validateFinalizeRequest(bootstrapSessionFinalizeRequest{
		NodeID:        testJoiningNodeID,
		AccountID:     accountID,
		AccountPubKey: accountPubKeyData,
		AccountMeta:   []byte(testInvalidJSON),
		AccountBlob:   blobData,
		PeerData:      buildSignedPeerFixture(t, privateKey, testBootstrapHost, 58103, accountID),
		MetaData:      buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 1),
	}); err == nil {
		t.Fatal("validateFinalizeRequest() error = nil, want account meta failure")
	}
}

func TestValidateFinalizeRequestRejectsInvalidAccountBlob(t *testing.T) {
	accountID, privateKey, _, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}

	if err := validateFinalizeRequest(bootstrapSessionFinalizeRequest{
		NodeID:        testJoiningNodeID,
		AccountID:     accountID,
		AccountPubKey: accountPubKeyData,
		AccountMeta:   accountMetaData,
		AccountBlob:   []byte(testInvalidJSON),
		PeerData:      buildSignedPeerFixture(t, privateKey, testBootstrapHost, 58103, accountID),
		MetaData:      buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 1),
	}); err == nil {
		t.Fatal("validateFinalizeRequest() error = nil, want account blob failure")
	}
}

func TestValidateFinalizeRequestRejectsAccountBlobPublicKeyMismatch(t *testing.T) {
	accountID, privateKey, _, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	_, _, otherBlobData, _, _, err := buildAccountFixtures(testUpdatedTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesOtherErrorFormat, err)
	}

	if err := validateFinalizeRequest(bootstrapSessionFinalizeRequest{
		NodeID:        testJoiningNodeID,
		AccountID:     accountID,
		AccountPubKey: accountPubKeyData,
		AccountMeta:   accountMetaData,
		AccountBlob:   otherBlobData,
		PeerData:      buildSignedPeerFixture(t, privateKey, testBootstrapHost, 58103, accountID),
		MetaData:      buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 1),
	}); err == nil {
		t.Fatal("validateFinalizeRequest() error = nil, want account blob pubkey mismatch")
	}
}

func TestValidateFinalizeRequestRejectsExistingRecordLoadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, privateKey, blobData, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	readManagedFile = func(path string) ([]byte, error) {
		if path == testPeerPath() {
			return nil, errors.New(testPeerReadFailedText)
		}
		return nil, os.ErrNotExist
	}

	err = validateFinalizeRequest(bootstrapSessionFinalizeRequest{
		NodeID:        testJoiningNodeID,
		AccountID:     accountID,
		AccountPubKey: accountPubKeyData,
		AccountMeta:   accountMetaData,
		AccountBlob:   blobData,
		PeerData:      buildSignedPeerFixture(t, privateKey, testBootstrapHost, 58103, accountID),
		MetaData:      buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 1),
	})
	if err == nil {
		t.Fatal("validateFinalizeRequest() error = nil, want existing record load failure")
	}
}

func TestValidateFinalizeRequestRejectsExistingPubKeyMismatch(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, privateKey, blobData, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	otherAccountID, otherPrivateKey, otherBlobData, otherPubKeyData, otherAccountMetaData, err := buildAccountFixtures(testUpdatedTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesOtherErrorFormat, err)
	}
	readManagedFile = func(path string) ([]byte, error) {
		switch path {
		case testPeerPath():
			return []byte(`{}`), nil
		case testMetaPath():
			return buildSignedMetaFixture(t, otherPrivateKey, testJoiningNodeID, otherAccountID, testBootstrapTimestamp, 1), nil
		case filepath.Join("network", "accounts", otherAccountID+pubkeyFileSuffix):
			return otherPubKeyData, nil
		case filepath.Join("network", "accounts", otherAccountID+metaFileSuffix):
			return otherAccountMetaData, nil
		case filepath.Join("network", "accounts", otherAccountID+accountBlobFileSuffix):
			return otherBlobData, nil
		default:
			return nil, os.ErrNotExist
		}
	}

	err = validateFinalizeRequest(bootstrapSessionFinalizeRequest{
		NodeID:        testJoiningNodeID,
		AccountID:     accountID,
		AccountPubKey: accountPubKeyData,
		AccountMeta:   accountMetaData,
		AccountBlob:   blobData,
		PeerData:      buildSignedPeerFixture(t, privateKey, testBootstrapHost, 58103, accountID),
		MetaData:      buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 2),
	})
	if err == nil {
		t.Fatal("validateFinalizeRequest() error = nil, want existing pubkey mismatch")
	}
}

func TestValidateFinalizeRequestRejectsTrustedPublicKeyMismatchFromExistingRecords(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	accountID, privateKey, blobData, accountPubKeyData, accountMetaData, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	otherAccountID, _, _, otherAccountPubKeyData, _, err := buildAccountFixtures(testUpdatedTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesOtherErrorFormat, err)
	}
	otherPubKey, err := accounts.VerifyPublicKeyFile(otherAccountID, otherAccountPubKeyData)
	if err != nil {
		t.Fatalf(testVerifyPublicKeyFileErrorFormat, err)
	}

	loadNodeRecords = func(string) (existingNodeRecords, error) {
		return existingNodeRecords{
			KnownNode: true,
			NodeMeta: metaFile{
				NodeID:    testJoiningNodeID,
				AccountID: accountID,
			},
			AccountPubKey: otherPubKey,
		}, nil
	}

	err = validateFinalizeRequest(bootstrapSessionFinalizeRequest{
		NodeID:        testJoiningNodeID,
		AccountID:     accountID,
		AccountPubKey: accountPubKeyData,
		AccountMeta:   accountMetaData,
		AccountBlob:   blobData,
		PeerData:      buildSignedPeerFixture(t, privateKey, testBootstrapHost, 58103, accountID),
		MetaData:      buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 1),
	})
	if err == nil {
		t.Fatal("validateFinalizeRequest() error = nil, want trusted pubkey mismatch")
	}
}

func TestVerifyPeerFileReturnsValidationErrors(t *testing.T) {
	accountID, privateKey, _, accountPubKeyData, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	accountPubKey, err := accounts.VerifyPublicKeyFile(accountID, accountPubKeyData)
	if err != nil {
		t.Fatalf(testVerifyPublicKeyFileErrorFormat, err)
	}

	if err := verifyPeerFile([]byte(testInvalidJSON), accountID, accountPubKey); err == nil {
		t.Fatal("verifyPeerFile() error = nil, want JSON failure")
	}

	badAccountData, err := json.Marshal(peerFile{
		IPv4:      testBootstrapHost,
		PORT:      58103,
		AccountID: testOtherAccountID,
		Signature: "bad",
	})
	if err != nil {
		t.Fatalf(testMarshalErrorFormat, err)
	}
	if err := verifyPeerFile(badAccountData, accountID, accountPubKey); err == nil {
		t.Fatal("verifyPeerFile() error = nil, want account mismatch")
	}

	zeroPortData, err := json.Marshal(peerFile{
		IPv4:      testBootstrapHost,
		PORT:      0,
		AccountID: accountID,
		Signature: "bad",
	})
	if err != nil {
		t.Fatalf(testMarshalErrorFormat, err)
	}
	if err := verifyPeerFile(zeroPortData, accountID, accountPubKey); err == nil {
		t.Fatal("verifyPeerFile() error = nil, want zero port failure")
	}

	validData := buildSignedPeerFixture(t, privateKey, testBootstrapHost, 58103, accountID)
	var valid peerFile
	if err := json.Unmarshal(validData, &valid); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	valid.Signature = "bad"
	invalidSigData, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf(testMarshalErrorFormat, err)
	}
	if err := verifyPeerFile(invalidSigData, accountID, accountPubKey); err == nil {
		t.Fatal("verifyPeerFile() error = nil, want signature failure")
	}
}

func TestVerifyMetaFileReturnsValidationErrors(t *testing.T) {
	accountID, privateKey, _, accountPubKeyData, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	accountPubKey, err := accounts.VerifyPublicKeyFile(accountID, accountPubKeyData)
	if err != nil {
		t.Fatalf(testVerifyPublicKeyFileErrorFormat, err)
	}

	if err := verifyMetaFile([]byte(testInvalidJSON), testJoiningNodeID, accountID, accountPubKey); err == nil {
		t.Fatal("verifyMetaFile() error = nil, want JSON failure")
	}

	wrongNode := mustMarshalJSON(t, metaFile{
		NodeID:    "other-node",
		AccountID: accountID,
		FirstSeen: testBootstrapTimestamp,
		Revision:  1,
		UpdatedAt: testBootstrapTimestamp,
		Signature: "bad",
	})
	if err := verifyMetaFile(wrongNode, testJoiningNodeID, accountID, accountPubKey); err == nil {
		t.Fatal("verifyMetaFile() error = nil, want node mismatch")
	}

	wrongAccount := mustMarshalJSON(t, metaFile{
		NodeID:    testJoiningNodeID,
		AccountID: testOtherAccountID,
		FirstSeen: testBootstrapTimestamp,
		Revision:  1,
		UpdatedAt: testBootstrapTimestamp,
		Signature: "bad",
	})
	if err := verifyMetaFile(wrongAccount, testJoiningNodeID, accountID, accountPubKey); err == nil {
		t.Fatal("verifyMetaFile() error = nil, want account mismatch")
	}

	zeroRevision := mustMarshalJSON(t, metaFile{
		NodeID:    testJoiningNodeID,
		AccountID: accountID,
		FirstSeen: testBootstrapTimestamp,
		Revision:  0,
		UpdatedAt: testBootstrapTimestamp,
		Signature: "bad",
	})
	if err := verifyMetaFile(zeroRevision, testJoiningNodeID, accountID, accountPubKey); err == nil {
		t.Fatal("verifyMetaFile() error = nil, want revision failure")
	}

	validData := buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 1)
	var valid metaFile
	if err := json.Unmarshal(validData, &valid); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	valid.Signature = "bad"
	invalidSigData := mustMarshalJSON(t, valid)
	if err := verifyMetaFile(invalidSigData, testJoiningNodeID, accountID, accountPubKey); err == nil {
		t.Fatal("verifyMetaFile() error = nil, want signature failure")
	}
}

func TestBuildMetaFileUsesSnakeCaseFields(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(testGenerateKeyErrorFormat, err)
	}

	metaData, err := buildMetaFile(testJoiningNodeID, testAccountID, testBootstrapTimestamp, 1, privateKey)
	if err != nil {
		t.Fatalf("buildMetaFile() error = %v", err)
	}

	jsonText := string(metaData)
	for _, wantedField := range []string{`"node_id"`, `"account_id"`, `"first_seen"`, `"updated_at"`, `"signature"`} {
		if !strings.Contains(jsonText, wantedField) {
			t.Fatalf("buildMetaFile() output missing %s: %s", wantedField, jsonText)
		}
	}
	for _, legacyField := range []string{`"Node_ID"`, `"AccountID"`, `"First Seen"`, `"Updated At"`, `"Signature"`} {
		if strings.Contains(jsonText, legacyField) {
			t.Fatalf("buildMetaFile() output still contains legacy field %s: %s", legacyField, jsonText)
		}
	}
}

func TestVerifyMetaFileAcceptsLegacyFieldNames(t *testing.T) {
	accountID, privateKey, _, accountPubKeyData, _, err := buildAccountFixtures(testBootstrapTimestamp)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	accountPubKey, err := accounts.VerifyPublicKeyFile(accountID, accountPubKeyData)
	if err != nil {
		t.Fatalf(testVerifyPublicKeyFileErrorFormat, err)
	}

	unsigned := legacyUnsignedMetaFile{
		NodeID:    testJoiningNodeID,
		AccountID: accountID,
		FirstSeen: testBootstrapTimestamp,
		Revision:  1,
		UpdatedAt: testUpdatedTimestamp,
	}
	signature, err := signPayload(privateKey, unsigned)
	if err != nil {
		t.Fatalf("signPayload() error = %v", err)
	}

	legacyData := mustMarshalJSON(t, legacyMetaFile{
		NodeID:    unsigned.NodeID,
		AccountID: unsigned.AccountID,
		FirstSeen: unsigned.FirstSeen,
		Revision:  unsigned.Revision,
		UpdatedAt: unsigned.UpdatedAt,
		Signature: signature,
	})
	if err := verifyMetaFile(legacyData, testJoiningNodeID, accountID, accountPubKey); err != nil {
		t.Fatalf("verifyMetaFile() error = %v", err)
	}
}

func TestVerifySignedPayloadReturnsErrors(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(testGenerateKeyErrorFormat, err)
	}

	if err := verifySignedPayload(make(chan int), "", publicKey); err == nil {
		t.Fatal("verifySignedPayload() error = nil, want marshal failure")
	}
	if err := verifySignedPayload(map[string]string{"ok": "yes"}, testInvalidBase64, publicKey); err == nil {
		t.Fatal("verifySignedPayload() error = nil, want decode failure")
	}
	if err := verifySignedPayload(map[string]string{"ok": "yes"}, base64Signature("bad"), publicKey); err == nil {
		t.Fatal("verifySignedPayload() error = nil, want signature failure")
	}
}

func TestWriteNetworkFilesReturnsWriteErrors(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	tests := []struct {
		name     string
		failPath string
	}{
		{name: "peer", failPath: peerRelativePath(testJoiningNodeID)},
		{name: "meta", failPath: metaRelativePath(testJoiningNodeID)},
		{name: "blob", failPath: accountBlobRelativePath(testAccountID)},
		{name: "pubkey", failPath: accountPubKeyRelativePath(testAccountID)},
		{name: "account-meta", failPath: accountMetaRelativePath(testAccountID)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			restore := stubBootstrapHooks(t)
			defer restore()

			writeManagedFile = func(path string, data []byte, perm os.FileMode) error {
				if path == tc.failPath {
					return errors.New(testWriteFailedText)
				}
				return nil
			}

			if _, _, _, err := writeNetworkFiles(testJoiningNodeID, testAccountID, []byte("peer"), []byte("meta"), []byte("blob"), []byte("pubkey"), []byte("account-meta")); err == nil {
				t.Fatal("writeNetworkFiles() error = nil, want write failure")
			}
		})
	}
}

func TestBuildCompletionArtifactsReturnsPeerSignError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	signPeerOrMeta = func(ed25519.PrivateKey, any) (string, error) {
		return "", errors.New(testSignFailedText)
	}

	_, err := buildCompletionArtifacts(&pendingSession{
		nodeID: testJoiningNodeID,
		response: bootstrapSessionStartResponse{
			ObservedIPv4: testBootstrapHost,
			Port:         58103,
		},
	}, material, "")
	if err == nil || err.Error() != testSignFailedText {
		t.Fatalf("buildCompletionArtifacts() error = %v, want %s", err, testSignFailedText)
	}
}

func TestBuildCompletionArtifactsReturnsMetaSignError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	material := testBootstrapMaterial(t)
	callCount := 0
	signPeerOrMeta = func(ed25519.PrivateKey, any) (string, error) {
		callCount++
		if callCount == 2 {
			return "", errors.New(testSignFailedText)
		}
		return "signature", nil
	}

	_, err := buildCompletionArtifacts(&pendingSession{
		nodeID: testJoiningNodeID,
		response: bootstrapSessionStartResponse{
			ObservedIPv4: testBootstrapHost,
			Port:         58103,
		},
	}, material, "")
	if err == nil || err.Error() != testSignFailedText {
		t.Fatalf("buildCompletionArtifacts() error = %v, want %s", err, testSignFailedText)
	}
}

func TestBuildCompletionArtifactsReturnsAccountMetaError(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(testGenerateKeyErrorFormat, err)
	}

	_, err = buildCompletionArtifacts(&pendingSession{
		nodeID: testJoiningNodeID,
		response: bootstrapSessionStartResponse{
			ObservedIPv4: testBootstrapHost,
			Port:         58103,
		},
	}, accounts.Material{
		AccountID:  "",
		PublicKey:  publicKey,
		PrivateKey: privateKey,
	}, "")
	if err == nil {
		t.Fatal("buildCompletionArtifacts() error = nil, want account meta failure")
	}
}

func TestStartBootstrapServiceLaunchesConnectionHandler(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testBootstrapNodeID }
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return loadBootstrapListFixture(t), nil
	}

	serverConn, clientConn := net.Pipe()
	listenBootstrap = func(int) (net.Listener, error) {
		return &scriptedListener{
			conns: []net.Conn{connWithRemoteAddr{
				Conn:       serverConn,
				remoteAddr: &net.TCPAddr{IP: net.ParseIP(testBootstrapHost), Port: 43001},
			}},
			acceptErr: errors.New("stop"),
		}, nil
	}

	done := make(chan error, 1)
	go func() {
		done <- startBootstrapService(context.Background())
	}()

	if _, err := clientConn.Write([]byte(testInvalidJSON)); err != nil {
		t.Fatalf(testWriteErrorFormat, err)
	}
	_ = clientConn.Close()

	if err := <-done; err == nil || err.Error() != "stop" {
		t.Fatalf("startBootstrapService() error = %v, want stop", err)
	}
}

func TestStartBootstrapServiceWrapsAcceptedConnection(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	resolveNodeID = func() string { return testBootstrapNodeID }
	fetchRemoteList = func(context.Context, string) ([]byte, error) {
		return loadBootstrapListFixture(t), nil
	}

	serverConn, clientConn := net.Pipe()
	listenBootstrap = func(int) (net.Listener, error) {
		return &scriptedListener{
			conns: []net.Conn{connWithRemoteAddr{
				Conn:       serverConn,
				remoteAddr: &net.TCPAddr{IP: net.ParseIP(testBootstrapHost), Port: 43001},
			}},
			acceptErr: errors.New("stop"),
		}, nil
	}

	wrapped := make(chan net.Conn, 1)
	wrapBootstrapConn = func(conn net.Conn) net.Conn {
		wrapped <- conn
		return conn
	}

	done := make(chan error, 1)
	go func() {
		done <- startBootstrapService(context.Background())
	}()

	if _, err := clientConn.Write([]byte(testInvalidJSON)); err != nil {
		t.Fatalf(testWriteErrorFormat, err)
	}
	_ = clientConn.Close()

	select {
	case got := <-wrapped:
		if got == nil {
			t.Fatal("startBootstrapService() wrapped nil connection")
		}
	case <-time.After(time.Second):
		t.Fatal("startBootstrapService() did not wrap the accepted connection")
	}

	if err := <-done; err == nil || err.Error() != "stop" {
		t.Fatalf("startBootstrapService() error = %v, want stop", err)
	}
}

func TestClearPendingSessionsHandlesEmptyAndExistingSessions(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	clearPendingSessions()

	conn := &stubConn{}
	storePendingSession(&pendingSession{id: testSessionID, conn: conn})
	clearPendingSessions()
	if conn.closeCount != 1 {
		t.Fatalf("clearPendingSessions() closeCount = %d, want %d", conn.closeCount, 1)
	}
}

func TestNewSessionIDReturnsRandomReadError(t *testing.T) {
	restore := stubBootstrapHooks(t)
	defer restore()

	wantErr := errors.New("random failed")
	randomSource = errReader{err: wantErr}

	if _, err := newSessionID(); !errors.Is(err, wantErr) {
		t.Fatalf("newSessionID() error = %v, want %v", err, wantErr)
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

type recoveryFixtureData struct {
	accountID         string
	publicKey         ed25519.PublicKey
	privateKey        ed25519.PrivateKey
	peerData          []byte
	metaData          []byte
	blobData          []byte
	accountPubKeyData []byte
	accountMetaData   []byte
	bundle            []recoveryBundleFile
	filesByPath       map[string][]byte
	material          accounts.Material
}

func recoveryBundleFixture(nodeID, accountID string, peerData, metaData, blobData, accountPubKeyData, accountMetaData []byte) []recoveryBundleFile {
	return []recoveryBundleFile{
		{Path: peerRelativePath(nodeID), Data: peerData},
		{Path: metaRelativePath(nodeID), Data: metaData},
		{Path: accountPubKeyRelativePath(accountID), Data: accountPubKeyData},
		{Path: accountMetaRelativePath(accountID), Data: accountMetaData},
		{Path: accountBlobRelativePath(accountID), Data: blobData},
	}
}

func buildRecoveryFixture(t *testing.T) recoveryFixtureData {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(testGenerateKeyErrorFormat, err)
	}
	accountID := accounts.AccountIDFromPublicKey(publicKey)
	blobData, err := accounts.BuildBlob(accountID, publicKey, privateKey, testRecoveryPassword)
	if err != nil {
		t.Fatalf(testBuildAccountFixturesErrorFormat, err)
	}
	accountPubKeyData := accounts.BuildPublicKeyFile(publicKey)
	accountMetaData, err := accounts.BuildMetaWithUsernameHash(accountID, publicKey, testBootstrapTimestamp, 1, testAliceHashKey, privateKey)
	if err != nil {
		t.Fatalf("BuildMetaWithUsernameHash() error = %v", err)
	}
	peerData := buildSignedPeerFixture(t, privateKey, testBootstrapHost, 58103, accountID)
	metaData := buildSignedMetaFixture(t, privateKey, testJoiningNodeID, accountID, testBootstrapTimestamp, 9)
	bundle := recoveryBundleFixture(testJoiningNodeID, accountID, peerData, metaData, blobData, accountPubKeyData, accountMetaData)
	filesByPath := map[string][]byte{}
	for _, file := range bundle {
		filesByPath[file.Path] = append([]byte(nil), file.Data...)
	}

	return recoveryFixtureData{
		accountID:         accountID,
		publicKey:         publicKey,
		privateKey:        privateKey,
		peerData:          peerData,
		metaData:          metaData,
		blobData:          blobData,
		accountPubKeyData: accountPubKeyData,
		accountMetaData:   accountMetaData,
		bundle:            bundle,
		filesByPath:       filesByPath,
		material: accounts.Material{
			AccountID:    accountID,
			LocalKeyPath: filepath.Join("local", "account", accountID+".key"),
			PublicKey:    publicKey,
			PrivateKey:   privateKey,
			BlobData:     blobData,
		},
	}
}

func cloneRecoveryFiles(files map[string][]byte) map[string][]byte {
	cloned := make(map[string][]byte, len(files))
	for path, data := range files {
		cloned[path] = append([]byte(nil), data...)
	}
	return cloned
}

func withoutRecoveryFile(files map[string][]byte, path string) map[string][]byte {
	cloned := cloneRecoveryFiles(files)
	delete(cloned, path)
	return cloned
}

func testBootstrapMaterial(t *testing.T) accounts.Material {
	t.Helper()

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf(testGenerateKeyErrorFormat, err)
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
	originalMarshalIndexJSON := marshalIndexJSON
	originalResolveNodeID := resolveNodeID
	originalCreateAccount := createAccount
	originalRecoverAccount := recoverAccount
	originalSaveLocalProfile := saveLocalProfile
	originalHTTPClient := httpClient
	originalFetchRemoteList := fetchRemoteList
	originalProbeEndpoint := probeEndpoint
	originalDialBootstrap := dialBootstrap
	originalListenBootstrap := listenBootstrap
	originalWrapBootstrapConn := wrapBootstrapConn
	originalLoadNodeRecords := loadNodeRecords
	originalLoadUsernameIndex := loadUsernameIndex
	originalSaveUsernameIndex := saveUsernameIndex
	originalCurrentTime := currentTime
	originalScheduleAfter := scheduleAfter
	originalSignPeerOrMeta := signPeerOrMeta
	originalRandomSource := randomSource
	originalServerStartOnce := serverStartOnce
	originalPendingSessions := pendingSessions

	ensureDataLayout = datamanager.EnsureLayout
	readDirectory = os.ReadDir
	readManagedFile = datamanager.ReadFile
	writeManagedFile = datamanager.WriteFile
	marshalIndexJSON = defaultMarshalIndexJSON
	resolveNodeID = nodeid.GetNodeID
	createAccount = accounts.Generate
	recoverAccount = accounts.Recover
	saveLocalProfile = func(string, string) (string, error) { return "", nil }
	httpClient = &http.Client{Timeout: 5 * time.Second}
	fetchRemoteList = fetchRemoteBootstrapList
	probeEndpoint = measureEndpointLatency
	dialBootstrap = dialBootstrapEndpoint
	listenBootstrap = listenBootstrapEndpoint
	wrapBootstrapConn = networkmanager.WrapConn
	loadNodeRecords = loadExistingNodeRecords
	loadUsernameIndex = loadUsernameIndexCache
	saveUsernameIndex = saveUsernameIndexCache
	currentTime = time.Now
	scheduleAfter = time.AfterFunc
	signPeerOrMeta = signPayload
	randomSource = rand.Reader
	serverStartOnce = sync.Once{}
	pendingSessions = map[string]*pendingSession{}

	return func() {
		clearPendingSessions()
		ensureDataLayout = originalEnsureDataLayout
		readDirectory = originalReadDirectory
		readManagedFile = originalReadManagedFile
		writeManagedFile = originalWriteManagedFile
		marshalIndexJSON = originalMarshalIndexJSON
		resolveNodeID = originalResolveNodeID
		createAccount = originalCreateAccount
		recoverAccount = originalRecoverAccount
		saveLocalProfile = originalSaveLocalProfile
		httpClient = originalHTTPClient
		fetchRemoteList = originalFetchRemoteList
		probeEndpoint = originalProbeEndpoint
		dialBootstrap = originalDialBootstrap
		listenBootstrap = originalListenBootstrap
		wrapBootstrapConn = originalWrapBootstrapConn
		loadNodeRecords = originalLoadNodeRecords
		loadUsernameIndex = originalLoadUsernameIndex
		saveUsernameIndex = originalSaveUsernameIndex
		currentTime = originalCurrentTime
		scheduleAfter = originalScheduleAfter
		signPeerOrMeta = originalSignPeerOrMeta
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

type scriptedListener struct {
	mu        sync.Mutex
	conns     []net.Conn
	acceptErr error
}

func (l *scriptedListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.conns) > 0 {
		conn := l.conns[0]
		l.conns = l.conns[1:]
		return conn, nil
	}

	return nil, l.acceptErr
}

func (l *scriptedListener) Close() error {
	return nil
}

func (l *scriptedListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4zero, Port: 58103}
}

type connWithRemoteAddr struct {
	net.Conn
	remoteAddr net.Addr
}

func (c connWithRemoteAddr) RemoteAddr() net.Addr {
	return c.remoteAddr
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type stubConn struct {
	readBuffer  bytes.Buffer
	writeBuffer bytes.Buffer
	readData    []byte
	readErr     error
	writeErr    error
	deadlineErr error
	closeErr    error
	closeCount  int
	remoteAddr  net.Addr
}

func (c *stubConn) Read(buffer []byte) (int, error) {
	if c.readBuffer.Len() == 0 && len(c.readData) > 0 {
		c.readBuffer.Write(c.readData)
		c.readData = nil
	}
	if c.readBuffer.Len() == 0 && c.readErr != nil {
		return 0, c.readErr
	}
	return c.readBuffer.Read(buffer)
}

func (c *stubConn) Write(buffer []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return c.writeBuffer.Write(buffer)
}

func (c *stubConn) Close() error {
	c.closeCount++
	return c.closeErr
}

func (c *stubConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4zero, Port: 0}
}

func (c *stubConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

func (c *stubConn) SetDeadline(time.Time) error {
	return c.deadlineErr
}

func (c *stubConn) SetReadDeadline(time.Time) error {
	return nil
}

func (c *stubConn) SetWriteDeadline(time.Time) error {
	return nil
}

type stubAddr struct {
	text string
}

func (a stubAddr) Network() string { return "tcp" }
func (a stubAddr) String() string  { return a.text }

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf(testMarshalErrorFormat, err)
	}
	return data
}

func base64Signature(text string) string {
	return base64.StdEncoding.EncodeToString([]byte(text))
}
