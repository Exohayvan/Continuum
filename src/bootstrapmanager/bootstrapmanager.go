package bootstrapmanager

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"continuum/src/accounts"
	"continuum/src/datamanager"
	"continuum/src/nodeid"
	"gopkg.in/yaml.v3"
)

const (
	BootstrapListURL       = "https://raw.githubusercontent.com/Exohayvan/Continuum/refs/heads/main/network/bootstrap-list.yaml"
	peerFileSuffix         = ".peer"
	metaFileSuffix         = ".meta"
	pubkeyFileSuffix       = ".pubkey"
	accountBlobFileSuffix  = ".blob"
	probeTimeout           = 1500 * time.Millisecond
	DefaultPort            = 58103
	bootstrapSessionTTL    = 2 * time.Minute
	bootstrapSessionIDSize = 16
)

type Node struct {
	Name                string `json:"name"`
	NodeID              string `json:"nodeId"`
	Host                string `json:"host"`
	Port                int    `json:"port"`
	Endpoint            string `json:"endpoint"`
	Reachable           bool   `json:"reachable"`
	LatencyMilliseconds int64  `json:"latencyMilliseconds"`
	Error               string `json:"error"`
}

type State struct {
	NeedsBootstrap bool   `json:"needsBootstrap"`
	PeerCount      int    `json:"peerCount"`
	Nodes          []Node `json:"nodes"`
	Error          string `json:"error"`
}

type ConnectResult struct {
	Connected         bool   `json:"connected"`
	AwaitingPassword  bool   `json:"awaitingPassword"`
	RecoveryAvailable bool   `json:"recoveryAvailable"`
	SessionID         string `json:"sessionId"`
	AccountID         string `json:"accountId"`
	ObservedIPv4      string `json:"observedIPv4"`
	Port              int    `json:"port"`
	Reachable         bool   `json:"reachable"`
	PeerFile          string `json:"peerFile"`
	MetaFile          string `json:"metaFile"`
	AccountBlobFile   string `json:"accountBlobFile"`
	LocalKeyFile      string `json:"localKeyFile"`
	Message           string `json:"message"`
}

type bootstrapList struct {
	Version int                            `yaml:"version"`
	Nodes   map[string]bootstrapNodeConfig `yaml:"nodes"`
}

type bootstrapNodeConfig struct {
	NodeID string `yaml:"node_id"`
	Host   string `yaml:"host"`
	Port   int    `yaml:"port"`
}

type unsignedPeerFile struct {
	IPv4      string `json:"IPv4"`
	PORT      int    `json:"PORT"`
	AccountID string `json:"AccountID"`
}

type peerFile struct {
	IPv4      string `json:"IPv4"`
	PORT      int    `json:"PORT"`
	AccountID string `json:"AccountID"`
	Signature string `json:"Signature"`
}

type unsignedMetaFile struct {
	NodeID    string `json:"Node_ID"`
	AccountID string `json:"AccountID"`
	FirstSeen string `json:"First Seen"`
	Revision  int    `json:"Revision"`
	UpdatedAt string `json:"Updated At"`
}

type metaFile struct {
	NodeID    string `json:"Node_ID"`
	AccountID string `json:"AccountID"`
	FirstSeen string `json:"First Seen"`
	Revision  int    `json:"Revision"`
	UpdatedAt string `json:"Updated At"`
	Signature string `json:"Signature"`
}

type bootstrapSessionStartRequest struct {
	Type       string `json:"type"`
	NodeID     string `json:"nodeId"`
	ListenPort int    `json:"listenPort"`
}

type bootstrapSessionStartResponse struct {
	ObservedIPv4      string `json:"observedIPv4"`
	Port              int    `json:"port"`
	Reachable         bool   `json:"reachable"`
	RecoveryAvailable bool   `json:"recoveryAvailable"`
	AccountID         string `json:"accountId"`
	AccountBlob       []byte `json:"accountBlob,omitempty"`
	FirstSeen         string `json:"firstSeen"`
	Revision          int    `json:"revision"`
	AccountCreatedAt  string `json:"accountCreatedAt"`
	AccountRevision   int    `json:"accountRevision"`
	Error             string `json:"error"`
}

type bootstrapSessionFinalizeRequest struct {
	Type          string `json:"type"`
	NodeID        string `json:"nodeId"`
	AccountID     string `json:"accountId"`
	PeerData      []byte `json:"peerData"`
	MetaData      []byte `json:"metaData"`
	AccountBlob   []byte `json:"accountBlob"`
	AccountPubKey []byte `json:"accountPubKey"`
	AccountMeta   []byte `json:"accountMeta"`
}

type bootstrapSessionFinalizeResponse struct {
	PeerFile        string `json:"peerFile"`
	MetaFile        string `json:"metaFile"`
	AccountBlobFile string `json:"accountBlobFile"`
	Error           string `json:"error"`
}

type pendingSession struct {
	id       string
	conn     net.Conn
	nodeID   string
	response bootstrapSessionStartResponse
	timer    *time.Timer
}

type existingNodeRecords struct {
	NodeMeta      metaFile
	AccountMeta   accounts.Meta
	AccountPubKey ed25519.PublicKey
	AccountBlob   []byte
	KnownNode     bool
}

var (
	ensureDataLayout = datamanager.EnsureLayout
	readDirectory    = os.ReadDir
	readManagedFile  = datamanager.ReadFile
	writeManagedFile = datamanager.WriteFile
	resolveNodeID    = nodeid.GetNodeID
	createAccount    = accounts.Generate
	recoverAccount   = accounts.Recover
	httpClient       = &http.Client{Timeout: 5 * time.Second}
	fetchRemoteList  = fetchRemoteBootstrapList
	probeEndpoint    = measureEndpointLatency
	dialBootstrap    = dialBootstrapEndpoint
	listenBootstrap  = listenBootstrapEndpoint
	currentTime      = time.Now
	randomSource     = rand.Reader
	serverStartOnce  sync.Once

	pendingSessionsMu sync.Mutex
	pendingSessions   = map[string]*pendingSession{}
)

var errInvalidBootstrapEndpoint = errors.New("invalid bootstrap endpoint")

func LoadState() State {
	dataPath, err := ensureDataLayout()
	if err != nil {
		return State{
			NeedsBootstrap: true,
			Error:          fmt.Sprintf("unable to prepare data layout: %v", err),
		}
	}

	peersPath := filepath.Join(dataPath, "network", "peers")
	peerCount, err := peerFileCount(peersPath)
	if err != nil {
		return State{
			NeedsBootstrap: true,
			Error:          fmt.Sprintf("unable to inspect peers directory: %v", err),
		}
	}

	if peerCount > 0 {
		return State{
			NeedsBootstrap: false,
			PeerCount:      peerCount,
		}
	}

	nodes, err := loadBootstrapNodes(context.Background())
	if err != nil {
		return State{
			NeedsBootstrap: true,
			PeerCount:      0,
			Error:          fmt.Sprintf("unable to load bootstrap nodes: %v", err),
		}
	}

	return State{
		NeedsBootstrap: true,
		PeerCount:      0,
		Nodes:          nodes,
	}
}

func peerFileCount(peersPath string) (int, error) {
	entries, err := readDirectory(peersPath)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), peerFileSuffix) {
			count++
		}
	}

	return count, nil
}

func loadBootstrapNodes(ctx context.Context) ([]Node, error) {
	list, err := loadBootstrapList(ctx)
	if err != nil {
		return nil, err
	}

	nodes := make([]Node, 0, len(list.Nodes))
	localNodeID := resolveNodeID()
	for name, config := range list.Nodes {
		nodes = append(nodes, Node{
			Name:     name,
			NodeID:   config.NodeID,
			Host:     config.Host,
			Port:     config.Port,
			Endpoint: net.JoinHostPort(config.Host, fmt.Sprintf("%d", config.Port)),
		})
	}

	var wg sync.WaitGroup
	for index := range nodes {
		wg.Add(1)
		go func(node *Node) {
			defer wg.Done()

			if node.Host == "" || node.Port <= 0 {
				node.Error = "invalid endpoint"
				return
			}

			probeHost := node.Host
			if node.NodeID != "" && node.NodeID == localNodeID {
				probeHost = "127.0.0.1"
			}

			latency, err := probeEndpoint(probeHost, node.Port)
			if err != nil {
				node.Error = err.Error()
				return
			}

			node.Reachable = true
			node.LatencyMilliseconds = max(1, latency.Milliseconds())
		}(&nodes[index])
	}
	wg.Wait()

	sort.SliceStable(nodes, func(left, right int) bool {
		leftNode := nodes[left]
		rightNode := nodes[right]

		if leftNode.Reachable != rightNode.Reachable {
			return leftNode.Reachable
		}
		if leftNode.Reachable && rightNode.Reachable && leftNode.LatencyMilliseconds != rightNode.LatencyMilliseconds {
			return leftNode.LatencyMilliseconds < rightNode.LatencyMilliseconds
		}

		return leftNode.Name < rightNode.Name
	})

	return nodes, nil
}

func loadBootstrapList(ctx context.Context) (bootstrapList, error) {
	data, err := fetchRemoteList(ctx, BootstrapListURL)
	if err != nil {
		return bootstrapList{}, err
	}

	var list bootstrapList
	if err := yaml.Unmarshal(data, &list); err != nil {
		return bootstrapList{}, err
	}

	return list, nil
}

func fetchRemoteBootstrapList(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "continuum-bootstrap")

	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bootstrap list returned %s", response.Status)
	}

	return io.ReadAll(response.Body)
}

func measureEndpointLatency(host string, port int) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	address := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	dialer := net.Dialer{}
	startedAt := time.Now()
	conn, err := dialer.DialContext(ctx, "tcp4", address)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	return time.Since(startedAt), nil
}

func StartService() {
	serverStartOnce.Do(func() {
		go func() {
			_ = startBootstrapService(context.Background())
		}()
	})
}

func Connect(host string, port int, bootstrapNodeID string) (ConnectResult, error) {
	if strings.TrimSpace(host) == "" || port <= 0 {
		return ConnectResult{}, errInvalidBootstrapEndpoint
	}

	clearPendingSessions()

	localNodeID := resolveNodeID()
	if localNodeID == "" {
		return ConnectResult{}, errors.New("unable to resolve local node id")
	}

	dialHost := host
	if bootstrapNodeID != "" && bootstrapNodeID == localNodeID {
		dialHost = "127.0.0.1"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := dialBootstrap(ctx, dialHost, port)
	if err != nil {
		return ConnectResult{}, err
	}

	if err := conn.SetDeadline(currentTime().Add(bootstrapSessionTTL)); err != nil {
		conn.Close()
		return ConnectResult{}, err
	}

	request := bootstrapSessionStartRequest{
		Type:       "start",
		NodeID:     localNodeID,
		ListenPort: defaultListenPort(localNodeID),
	}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		conn.Close()
		return ConnectResult{}, err
	}

	var response bootstrapSessionStartResponse
	if err := json.NewDecoder(conn).Decode(&response); err != nil {
		conn.Close()
		return ConnectResult{}, err
	}
	if response.Error != "" {
		conn.Close()
		return ConnectResult{}, errors.New(response.Error)
	}
	if response.ObservedIPv4 == "" || response.Port <= 0 {
		conn.Close()
		return ConnectResult{}, errors.New("bootstrap response did not include a usable endpoint")
	}

	sessionID, err := newSessionID()
	if err != nil {
		conn.Close()
		return ConnectResult{}, err
	}

	session := &pendingSession{
		id:       sessionID,
		conn:     conn,
		nodeID:   localNodeID,
		response: response,
	}
	session.timer = time.AfterFunc(bootstrapSessionTTL, func() {
		removePendingSession(sessionID)
	})
	storePendingSession(session)

	result := ConnectResult{
		AwaitingPassword:  true,
		RecoveryAvailable: response.RecoveryAvailable,
		SessionID:         sessionID,
		AccountID:         response.AccountID,
		ObservedIPv4:      response.ObservedIPv4,
		Port:              response.Port,
		Reachable:         response.Reachable,
	}
	if response.RecoveryAvailable {
		result.Message = fmt.Sprintf("Account %s was found on the bootstrap node. Enter its password to recover the local signing key.", response.AccountID)
	} else {
		result.Message = "No account was found for this NodeID. Set a password to create and encrypt a new account blob."
	}

	return result, nil
}

func Complete(sessionID, password string) (ConnectResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return ConnectResult{}, errors.New("bootstrap session id is required")
	}
	if strings.TrimSpace(password) == "" {
		return ConnectResult{}, errors.New("bootstrap password is required")
	}

	session, ok := getPendingSession(sessionID)
	if !ok {
		return ConnectResult{}, errors.New("bootstrap session expired or was not found")
	}

	var (
		material accounts.Material
		err      error
	)
	if session.response.RecoveryAvailable {
		material, err = recoverAccount(session.response.AccountBlob, password)
	} else {
		material, err = createAccount(password)
	}
	if err != nil {
		return ConnectResult{}, err
	}

	firstSeen := session.response.FirstSeen
	if strings.TrimSpace(firstSeen) == "" {
		firstSeen = currentTime().UTC().Format(time.RFC3339)
	}
	revision := session.response.Revision
	if revision <= 0 {
		revision = 1
	}
	accountCreatedAt := session.response.AccountCreatedAt
	if strings.TrimSpace(accountCreatedAt) == "" {
		accountCreatedAt = currentTime().UTC().Format(time.RFC3339)
	}
	accountRevision := session.response.AccountRevision
	if accountRevision <= 0 {
		accountRevision = 1
	}

	peerData, err := buildPeerFile(session.response.ObservedIPv4, session.response.Port, material.AccountID, material.PrivateKey)
	if err != nil {
		return ConnectResult{}, err
	}
	metaData, err := buildMetaFile(session.nodeID, material.AccountID, firstSeen, revision, material.PrivateKey)
	if err != nil {
		return ConnectResult{}, err
	}
	accountPubKeyData := accounts.BuildPublicKeyFile(material.PublicKey)
	accountMetaData, err := accounts.BuildMeta(material.AccountID, material.PublicKey, accountCreatedAt, accountRevision, material.PrivateKey)
	if err != nil {
		return ConnectResult{}, err
	}

	peerPath, metaPath, accountBlobPath, err := writeNetworkFiles(session.nodeID, material.AccountID, peerData, metaData, material.BlobData, accountPubKeyData, accountMetaData)
	if err != nil {
		return ConnectResult{}, err
	}

	finalizeRequest := bootstrapSessionFinalizeRequest{
		Type:          "finalize",
		NodeID:        session.nodeID,
		AccountID:     material.AccountID,
		PeerData:      peerData,
		MetaData:      metaData,
		AccountBlob:   material.BlobData,
		AccountPubKey: accountPubKeyData,
		AccountMeta:   accountMetaData,
	}
	if err := json.NewEncoder(session.conn).Encode(finalizeRequest); err != nil {
		removePendingSession(sessionID)
		return ConnectResult{}, err
	}

	var finalizeResponse bootstrapSessionFinalizeResponse
	if err := json.NewDecoder(session.conn).Decode(&finalizeResponse); err != nil {
		removePendingSession(sessionID)
		return ConnectResult{}, err
	}
	removePendingSession(sessionID)

	if finalizeResponse.Error != "" {
		return ConnectResult{}, errors.New(finalizeResponse.Error)
	}

	result := ConnectResult{
		Connected:       true,
		AccountID:       material.AccountID,
		ObservedIPv4:    session.response.ObservedIPv4,
		Port:            session.response.Port,
		Reachable:       session.response.Reachable,
		PeerFile:        peerPath,
		MetaFile:        metaPath,
		AccountBlobFile: accountBlobPath,
		LocalKeyFile:    material.LocalKeyPath,
	}
	if session.response.Reachable {
		result.Message = fmt.Sprintf("Bootstrap completed. Saved %s, %s, %s, and %s.", material.LocalKeyPath, peerPath, metaPath, accountBlobPath)
	} else {
		result.Message = fmt.Sprintf("Bootstrap completed in degraded mode. Saved %s, %s, %s, and %s.", material.LocalKeyPath, peerPath, metaPath, accountBlobPath)
	}

	return result, nil
}

func startBootstrapService(ctx context.Context) error {
	list, err := loadBootstrapList(ctx)
	if err != nil {
		return err
	}

	localNodeID := resolveNodeID()
	if localNodeID == "" {
		return nil
	}

	port, ok := bootstrapPortForNode(list, localNodeID)
	if !ok || port <= 0 {
		return nil
	}

	listener, err := listenBootstrap(port)
	if err != nil {
		return err
	}
	defer listener.Close()

	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}

		go handleBootstrapConnection(conn)
	}
}

func handleBootstrapConnection(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(currentTime().Add(bootstrapSessionTTL))

	var startRequest bootstrapSessionStartRequest
	if err := json.NewDecoder(conn).Decode(&startRequest); err != nil {
		return
	}

	startResponse := buildBootstrapStartResponse(conn.RemoteAddr(), startRequest)
	if err := json.NewEncoder(conn).Encode(startResponse); err != nil {
		return
	}
	if startResponse.Error != "" {
		return
	}

	var finalizeRequest bootstrapSessionFinalizeRequest
	if err := json.NewDecoder(conn).Decode(&finalizeRequest); err != nil {
		return
	}

	response := bootstrapSessionFinalizeResponse{}
	if err := validateFinalizeRequest(finalizeRequest); err != nil {
		response.Error = err.Error()
		_ = json.NewEncoder(conn).Encode(response)
		return
	}

	peerPath, metaPath, blobPath, err := writeNetworkFiles(
		finalizeRequest.NodeID,
		finalizeRequest.AccountID,
		finalizeRequest.PeerData,
		finalizeRequest.MetaData,
		finalizeRequest.AccountBlob,
		finalizeRequest.AccountPubKey,
		finalizeRequest.AccountMeta,
	)
	if err != nil {
		response.Error = err.Error()
		_ = json.NewEncoder(conn).Encode(response)
		return
	}

	response.PeerFile = peerPath
	response.MetaFile = metaPath
	response.AccountBlobFile = blobPath
	_ = json.NewEncoder(conn).Encode(response)
}

func bootstrapPortForNode(list bootstrapList, localNodeID string) (int, bool) {
	for _, config := range list.Nodes {
		if config.NodeID == localNodeID {
			if config.Port <= 0 {
				return DefaultPort, true
			}
			return config.Port, true
		}
	}

	return 0, false
}

func defaultListenPort(localNodeID string) int {
	list, err := loadBootstrapList(context.Background())
	if err == nil {
		if port, ok := bootstrapPortForNode(list, localNodeID); ok && port > 0 {
			return port
		}
	}

	return DefaultPort
}

func buildBootstrapStartResponse(remoteAddr net.Addr, request bootstrapSessionStartRequest) bootstrapSessionStartResponse {
	observedIPv4, err := extractObservedIPv4(remoteAddr)
	if err != nil {
		return bootstrapSessionStartResponse{Error: err.Error()}
	}

	response := bootstrapSessionStartResponse{
		ObservedIPv4:     observedIPv4,
		Port:             request.ListenPort,
		FirstSeen:        currentTime().UTC().Format(time.RFC3339),
		Revision:         1,
		AccountCreatedAt: currentTime().UTC().Format(time.RFC3339),
		AccountRevision:  1,
	}
	if request.ListenPort > 0 {
		if _, err := probeEndpoint(observedIPv4, request.ListenPort); err == nil {
			response.Reachable = true
		}
	}

	existingRecords, err := loadExistingNodeRecords(request.NodeID)
	if err != nil {
		response.Error = err.Error()
		return response
	}

	if existingRecords.KnownNode && strings.TrimSpace(existingRecords.NodeMeta.FirstSeen) != "" {
		response.FirstSeen = existingRecords.NodeMeta.FirstSeen
	}
	if existingRecords.KnownNode && existingRecords.NodeMeta.Revision > 0 {
		response.Revision = existingRecords.NodeMeta.Revision + 1
	}
	if strings.TrimSpace(existingRecords.AccountMeta.CreatedAt) != "" {
		response.AccountCreatedAt = existingRecords.AccountMeta.CreatedAt
	}
	if existingRecords.AccountMeta.Revision > 0 {
		response.AccountRevision = existingRecords.AccountMeta.Revision + 1
	}
	if strings.TrimSpace(existingRecords.NodeMeta.AccountID) != "" && len(existingRecords.AccountBlob) > 0 {
		response.RecoveryAvailable = true
		response.AccountID = existingRecords.NodeMeta.AccountID
		response.AccountBlob = existingRecords.AccountBlob
	}

	return response
}

func loadExistingNodeRecords(nodeID string) (existingNodeRecords, error) {
	if strings.TrimSpace(nodeID) == "" {
		return existingNodeRecords{}, nil
	}

	peerPath := peerRelativePath(nodeID)
	if _, err := readManagedFile(peerPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return existingNodeRecords{}, nil
		}
		return existingNodeRecords{}, err
	}

	metaPath := metaRelativePath(nodeID)
	metaData, err := readManagedFile(metaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return existingNodeRecords{KnownNode: true}, nil
		}
		return existingNodeRecords{}, err
	}

	var existingMeta metaFile
	if err := json.Unmarshal(metaData, &existingMeta); err != nil {
		return existingNodeRecords{}, err
	}
	if strings.TrimSpace(existingMeta.AccountID) == "" {
		return existingNodeRecords{NodeMeta: existingMeta, KnownNode: true}, nil
	}

	accountPubKeyPath := accountPubKeyRelativePath(existingMeta.AccountID)
	accountPubKeyData, err := readManagedFile(accountPubKeyPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return existingNodeRecords{NodeMeta: existingMeta, KnownNode: true}, nil
		}
		return existingNodeRecords{}, err
	}
	accountPubKey, err := accounts.VerifyPublicKeyFile(existingMeta.AccountID, accountPubKeyData)
	if err != nil {
		return existingNodeRecords{}, err
	}

	accountMetaPath := accountMetaRelativePath(existingMeta.AccountID)
	accountMetaData, err := readManagedFile(accountMetaPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return existingNodeRecords{NodeMeta: existingMeta, AccountPubKey: accountPubKey, KnownNode: true}, nil
		}
		return existingNodeRecords{}, err
	}
	accountMeta, err := accounts.VerifyMeta(existingMeta.AccountID, accountPubKey, accountMetaData)
	if err != nil {
		return existingNodeRecords{}, err
	}
	if err := verifyMetaFile(metaData, nodeID, existingMeta.AccountID, accountPubKey); err != nil {
		return existingNodeRecords{}, err
	}

	accountBlobPath := accountBlobRelativePath(existingMeta.AccountID)
	blobData, err := readManagedFile(accountBlobPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return existingNodeRecords{
				NodeMeta:      existingMeta,
				AccountMeta:   accountMeta,
				AccountPubKey: accountPubKey,
				KnownNode:     true,
			}, nil
		}
		return existingNodeRecords{}, err
	}
	_, blobPublicKey, err := accounts.ValidateBlob(blobData)
	if err != nil {
		return existingNodeRecords{}, err
	}
	if !ed25519.PublicKey(blobPublicKey).Equal(accountPubKey) {
		return existingNodeRecords{}, errors.New("account blob public key does not match trusted account pubkey")
	}

	return existingNodeRecords{
		NodeMeta:      existingMeta,
		AccountMeta:   accountMeta,
		AccountPubKey: accountPubKey,
		AccountBlob:   blobData,
		KnownNode:     true,
	}, nil
}

func extractObservedIPv4(remoteAddr net.Addr) (string, error) {
	if remoteAddr == nil {
		return "", errors.New("missing remote address")
	}

	host, _, err := net.SplitHostPort(remoteAddr.String())
	if err != nil {
		return "", err
	}

	ip := net.ParseIP(host)
	if ip == nil || ip.To4() == nil {
		return "", errors.New("remote address is not IPv4")
	}

	return ip.String(), nil
}

func buildPeerFile(observedIPv4 string, port int, accountID string, privateKey ed25519.PrivateKey) ([]byte, error) {
	unsigned := unsignedPeerFile{
		IPv4:      observedIPv4,
		PORT:      port,
		AccountID: accountID,
	}
	signature, err := signPayload(privateKey, unsigned)
	if err != nil {
		return nil, err
	}

	return json.MarshalIndent(peerFile{
		IPv4:      unsigned.IPv4,
		PORT:      unsigned.PORT,
		AccountID: unsigned.AccountID,
		Signature: signature,
	}, "", "  ")
}

func buildMetaFile(nodeID, accountID, firstSeen string, revision int, privateKey ed25519.PrivateKey) ([]byte, error) {
	unsigned := unsignedMetaFile{
		NodeID:    nodeID,
		AccountID: accountID,
		FirstSeen: firstSeen,
		Revision:  revision,
		UpdatedAt: currentTime().UTC().Format(time.RFC3339),
	}
	signature, err := signPayload(privateKey, unsigned)
	if err != nil {
		return nil, err
	}

	return json.MarshalIndent(metaFile{
		NodeID:    unsigned.NodeID,
		AccountID: unsigned.AccountID,
		FirstSeen: unsigned.FirstSeen,
		Revision:  unsigned.Revision,
		UpdatedAt: unsigned.UpdatedAt,
		Signature: signature,
	}, "", "  ")
}

func signPayload(privateKey ed25519.PrivateKey, payload any) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	return base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, data)), nil
}

func validateFinalizeRequest(request bootstrapSessionFinalizeRequest) error {
	accountPubKey, err := accounts.VerifyPublicKeyFile(request.AccountID, request.AccountPubKey)
	if err != nil {
		return err
	}
	if _, err := accounts.VerifyMeta(request.AccountID, accountPubKey, request.AccountMeta); err != nil {
		return err
	}
	if _, blobPublicKey, err := accounts.ValidateBlob(request.AccountBlob); err != nil {
		return err
	} else if !ed25519.PublicKey(blobPublicKey).Equal(accountPubKey) {
		return errors.New("account blob public key does not match trusted account pubkey")
	}
	if err := verifyPeerFile(request.PeerData, request.AccountID, accountPubKey); err != nil {
		return err
	}
	if err := verifyMetaFile(request.MetaData, request.NodeID, request.AccountID, accountPubKey); err != nil {
		return err
	}

	existingRecords, err := loadExistingNodeRecords(request.NodeID)
	if err != nil {
		return err
	}
	if existingRecords.KnownNode && strings.TrimSpace(existingRecords.NodeMeta.AccountID) != "" && existingRecords.NodeMeta.AccountID != request.AccountID {
		return errors.New("node is already bound to a different account")
	}
	if existingRecords.KnownNode && len(existingRecords.AccountPubKey) > 0 && !existingRecords.AccountPubKey.Equal(accountPubKey) {
		return errors.New("node is already bound to a different account public key")
	}

	return nil
}

func verifyPeerFile(data []byte, accountID string, publicKey ed25519.PublicKey) error {
	var file peerFile
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	if file.AccountID != strings.TrimSpace(accountID) {
		return errors.New("peer file account id does not match expected account")
	}
	if file.PORT <= 0 {
		return errors.New("peer file port must be positive")
	}
	unsigned := unsignedPeerFile{
		IPv4:      file.IPv4,
		PORT:      file.PORT,
		AccountID: file.AccountID,
	}
	return verifySignedPayload(unsigned, file.Signature, publicKey)
}

func verifyMetaFile(data []byte, nodeID, accountID string, publicKey ed25519.PublicKey) error {
	var file metaFile
	if err := json.Unmarshal(data, &file); err != nil {
		return err
	}
	if file.NodeID != strings.TrimSpace(nodeID) {
		return errors.New("node meta node id does not match expected node")
	}
	if file.AccountID != strings.TrimSpace(accountID) {
		return errors.New("node meta account id does not match expected account")
	}
	if file.Revision <= 0 {
		return errors.New("node meta revision must be positive")
	}
	unsigned := unsignedMetaFile{
		NodeID:    file.NodeID,
		AccountID: file.AccountID,
		FirstSeen: file.FirstSeen,
		Revision:  file.Revision,
		UpdatedAt: file.UpdatedAt,
	}
	return verifySignedPayload(unsigned, file.Signature, publicKey)
}

func verifySignedPayload(payload any, signature string, publicKey ed25519.PublicKey) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	decodedSignature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return err
	}
	if !ed25519.Verify(publicKey, data, decodedSignature) {
		return errors.New("signature verification failed")
	}

	return nil
}

func writeNetworkFiles(nodeID, accountID string, peerData, metaData, accountBlobData, accountPubKeyData, accountMetaData []byte) (string, string, string, error) {
	peerPath := peerRelativePath(nodeID)
	if err := writeManagedFile(peerPath, peerData, 0o644); err != nil {
		return "", "", "", err
	}

	metaPath := metaRelativePath(nodeID)
	if err := writeManagedFile(metaPath, metaData, 0o644); err != nil {
		return "", "", "", err
	}

	accountBlobPath := accountBlobRelativePath(accountID)
	if err := writeManagedFile(accountBlobPath, accountBlobData, 0o644); err != nil {
		return "", "", "", err
	}

	accountPubKeyPath := accountPubKeyRelativePath(accountID)
	if err := writeManagedFile(accountPubKeyPath, accountPubKeyData, accounts.PubkeyFilePerm()); err != nil {
		return "", "", "", err
	}

	accountMetaPath := accountMetaRelativePath(accountID)
	if err := writeManagedFile(accountMetaPath, accountMetaData, 0o644); err != nil {
		return "", "", "", err
	}

	return peerPath, metaPath, accountBlobPath, nil
}

func peerRelativePath(nodeID string) string {
	return filepath.Join("network", "peers", strings.TrimSpace(nodeID)+peerFileSuffix)
}

func metaRelativePath(nodeID string) string {
	return filepath.Join("network", "peers", strings.TrimSpace(nodeID)+metaFileSuffix)
}

func accountBlobRelativePath(accountID string) string {
	return filepath.Join("network", "accounts", strings.TrimSpace(accountID)+accountBlobFileSuffix)
}

func accountPubKeyRelativePath(accountID string) string {
	return filepath.Join("network", "accounts", strings.TrimSpace(accountID)+pubkeyFileSuffix)
}

func accountMetaRelativePath(accountID string) string {
	return filepath.Join("network", "accounts", strings.TrimSpace(accountID)+metaFileSuffix)
}

func dialBootstrapEndpoint(ctx context.Context, host string, port int) (net.Conn, error) {
	address := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	dialer := net.Dialer{}
	return dialer.DialContext(ctx, "tcp4", address)
}

func listenBootstrapEndpoint(port int) (net.Listener, error) {
	address := net.JoinHostPort("0.0.0.0", fmt.Sprintf("%d", port))
	return net.Listen("tcp4", address)
}

func storePendingSession(session *pendingSession) {
	pendingSessionsMu.Lock()
	defer pendingSessionsMu.Unlock()
	pendingSessions[session.id] = session
}

func getPendingSession(sessionID string) (*pendingSession, bool) {
	pendingSessionsMu.Lock()
	defer pendingSessionsMu.Unlock()
	session, ok := pendingSessions[sessionID]
	return session, ok
}

func removePendingSession(sessionID string) {
	pendingSessionsMu.Lock()
	session, ok := pendingSessions[sessionID]
	if ok {
		delete(pendingSessions, sessionID)
	}
	pendingSessionsMu.Unlock()

	if !ok {
		return
	}
	if session.timer != nil {
		session.timer.Stop()
	}
	_ = session.conn.Close()
}

func clearPendingSessions() {
	pendingSessionsMu.Lock()
	sessionIDs := make([]string, 0, len(pendingSessions))
	for sessionID := range pendingSessions {
		sessionIDs = append(sessionIDs, sessionID)
	}
	pendingSessionsMu.Unlock()

	for _, sessionID := range sessionIDs {
		removePendingSession(sessionID)
	}
}

func newSessionID() (string, error) {
	randomBytes := make([]byte, bootstrapSessionIDSize)
	if _, err := io.ReadFull(randomSource, randomBytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(randomBytes), nil
}
