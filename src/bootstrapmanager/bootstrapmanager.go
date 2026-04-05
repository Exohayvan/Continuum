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
	"continuum/src/networkmanager"
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
	usernameIndexPath      = "cache/username-index.map"
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
	NodeID    string `json:"node_id"`
	AccountID string `json:"account_id"`
	FirstSeen string `json:"first_seen"`
	Revision  int    `json:"revision"`
	UpdatedAt string `json:"updated_at"`
}

type metaFile struct {
	NodeID    string `json:"node_id"`
	AccountID string `json:"account_id"`
	FirstSeen string `json:"first_seen"`
	Revision  int    `json:"revision"`
	UpdatedAt string `json:"updated_at"`
	Signature string `json:"signature"`
}

type legacyUnsignedMetaFile struct {
	NodeID    string `json:"Node_ID"`
	AccountID string `json:"AccountID"`
	FirstSeen string `json:"First Seen"`
	Revision  int    `json:"Revision"`
	UpdatedAt string `json:"Updated At"`
}

type legacyMetaFile struct {
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
	ObservedIPv4      string               `json:"observedIPv4"`
	Port              int                  `json:"port"`
	Reachable         bool                 `json:"reachable"`
	RecoveryAvailable bool                 `json:"recoveryAvailable"`
	AccountID         string               `json:"accountId"`
	UsernameHash      string               `json:"usernameHash,omitempty"`
	AccountBlob       []byte               `json:"accountBlob,omitempty"`
	RecoveryBundle    []recoveryBundleFile `json:"recoveryBundle,omitempty"`
	FirstSeen         string               `json:"firstSeen"`
	Revision          int                  `json:"revision"`
	AccountCreatedAt  string               `json:"accountCreatedAt"`
	AccountRevision   int                  `json:"accountRevision"`
	Error             string               `json:"error"`
}

type recoveryBundleFile struct {
	Path string `json:"path"`
	Data []byte `json:"data"`
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
	PeerData          []byte
	NodeMetaData      []byte
	AccountPubKeyData []byte
	AccountMetaData   []byte
	NodeMeta          metaFile
	AccountMeta       accounts.Meta
	AccountPubKey     ed25519.PublicKey
	AccountBlob       []byte
	KnownNode         bool
}

type usernameIndex map[string][]string

func (file *metaFile) UnmarshalJSON(data []byte) error {
	type metaFileAlias metaFile

	var current metaFileAlias
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}

	var legacy legacyMetaFile
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}

	file.NodeID = firstNonEmpty(current.NodeID, legacy.NodeID)
	file.AccountID = firstNonEmpty(current.AccountID, legacy.AccountID)
	file.FirstSeen = firstNonEmpty(current.FirstSeen, legacy.FirstSeen)
	file.Revision = firstPositive(current.Revision, legacy.Revision)
	file.UpdatedAt = firstNonEmpty(current.UpdatedAt, legacy.UpdatedAt)
	file.Signature = firstNonEmpty(current.Signature, legacy.Signature)
	return nil
}

func defaultMarshalIndexJSON(index usernameIndex) ([]byte, error) {
	return json.MarshalIndent(index, "", "  ")
}

var (
	ensureDataLayout  = datamanager.EnsureLayout
	readDirectory     = os.ReadDir
	readManagedFile   = datamanager.ReadFile
	writeManagedFile  = datamanager.WriteFile
	marshalIndexJSON  func(usernameIndex) ([]byte, error)
	resolveNodeID     = nodeid.GetNodeID
	createAccount     = accounts.Generate
	recoverAccount    = accounts.Recover
	saveLocalProfile  = accounts.SaveLocalProfile
	httpClient        = &http.Client{Timeout: 5 * time.Second}
	fetchRemoteList   = fetchRemoteBootstrapList
	probeEndpoint     = measureEndpointLatency
	dialBootstrap     = dialBootstrapEndpoint
	listenBootstrap   = listenBootstrapEndpoint
	wrapBootstrapConn = networkmanager.WrapConn
	loadNodeRecords   = loadExistingNodeRecords
	loadUsernameIndex = loadUsernameIndexCache
	saveUsernameIndex = saveUsernameIndexCache
	currentTime       = time.Now
	scheduleAfter     = time.AfterFunc
	signPeerOrMeta    = signPayload
	randomSource      = rand.Reader
	serverStartOnce   sync.Once

	pendingSessionsMu sync.Mutex
	pendingSessions   = map[string]*pendingSession{}
)

func init() {
	marshalIndexJSON = defaultMarshalIndexJSON
}

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

	nodes := bootstrapNodesFromList(list)
	probeBootstrapNodes(nodes, resolveNodeID())
	sortBootstrapNodes(nodes)
	return nodes, nil
}

func bootstrapNodesFromList(list bootstrapList) []Node {
	nodes := make([]Node, 0, len(list.Nodes))
	for name, config := range list.Nodes {
		nodes = append(nodes, Node{
			Name:     name,
			NodeID:   config.NodeID,
			Host:     config.Host,
			Port:     config.Port,
			Endpoint: net.JoinHostPort(config.Host, fmt.Sprintf("%d", config.Port)),
		})
	}

	return nodes
}

func probeBootstrapNodes(nodes []Node, localNodeID string) {
	var wg sync.WaitGroup
	for index := range nodes {
		wg.Add(1)
		go func(node *Node) {
			defer wg.Done()
			probeBootstrapNode(node, localNodeID)
		}(&nodes[index])
	}
	wg.Wait()
}

func probeBootstrapNode(node *Node, localNodeID string) {
	if node.Host == "" || node.Port <= 0 {
		node.Error = "invalid endpoint"
		return
	}

	latency, err := probeEndpoint(bootstrapProbeHost(*node, localNodeID), node.Port)
	if err != nil {
		node.Error = err.Error()
		return
	}

	node.Reachable = true
	node.LatencyMilliseconds = max(1, latency.Milliseconds())
}

func bootstrapProbeHost(node Node, localNodeID string) string {
	if node.NodeID != "" && node.NodeID == localNodeID {
		return "127.0.0.1"
	}

	return node.Host
}

func sortBootstrapNodes(nodes []Node) {
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
	conn = wrapBootstrapConn(conn)

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
	session.timer = scheduleAfter(bootstrapSessionTTL, func() {
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
	if err := validateCompletionInputs(sessionID, password); err != nil {
		return ConnectResult{}, err
	}

	session, err := pendingSessionForCompletion(sessionID)
	if err != nil {
		return ConnectResult{}, err
	}

	if session.response.RecoveryAvailable {
		return restoreRecoveryCompletion(sessionID, session, password)
	}

	material, err := completionMaterial(session.response, password)
	if err != nil {
		return ConnectResult{}, err
	}

	return finalizeCompletion(sessionID, session, material, "")
}

func Recover(sessionID, password string) (ConnectResult, error) {
	return Complete(sessionID, password)
}

func Register(sessionID, username, password string) (ConnectResult, error) {
	if err := validateCompletionInputs(sessionID, password); err != nil {
		return ConnectResult{}, err
	}

	normalizedUsername, err := normalizeUsername(username)
	if err != nil {
		return ConnectResult{}, err
	}
	usernameHash := accounts.UsernameHash(normalizedUsername)

	session, err := pendingSessionForCompletion(sessionID)
	if err != nil {
		return ConnectResult{}, err
	}

	material, err := createAccount(password)
	if err != nil {
		return ConnectResult{}, err
	}

	index, err := loadUsernameIndex()
	if err != nil {
		return ConnectResult{}, err
	}

	if existingAccountIDs := index.accountIDsForHash(usernameHash); len(existingAccountIDs) > 0 && !containsAccountID(existingAccountIDs, material.AccountID) {
		return ConnectResult{}, errors.New("username already exists")
	}

	return finalizeUsernameCompletion(sessionID, session, material, normalizedUsername, usernameHash, index, "Registered %s with account %s. %s")
}

func Login(sessionID, username, password string) (ConnectResult, error) {
	if err := validateCompletionInputs(sessionID, password); err != nil {
		return ConnectResult{}, err
	}

	normalizedUsername, err := normalizeUsername(username)
	if err != nil {
		return ConnectResult{}, err
	}
	usernameHash := accounts.UsernameHash(normalizedUsername)

	session, err := pendingSessionForCompletion(sessionID)
	if err != nil {
		return ConnectResult{}, err
	}

	index, err := loadUsernameIndex()
	if err != nil {
		return ConnectResult{}, err
	}

	accountIDs := index.accountIDsForHash(usernameHash)
	if len(accountIDs) == 0 {
		return ConnectResult{}, errors.New("username does not exist")
	}
	if len(accountIDs) > 1 {
		return ConnectResult{}, errors.New("username matches multiple accounts")
	}
	accountID := accountIDs[0]

	blobData, err := readManagedFile(accountBlobRelativePath(accountID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ConnectResult{}, errors.New("account blob for username was not found locally")
		}
		return ConnectResult{}, err
	}

	material, err := recoverAccount(blobData, password)
	if err != nil {
		return ConnectResult{}, err
	}
	if material.AccountID != accountID {
		return ConnectResult{}, errors.New("username account mapping does not match recovered account")
	}

	return finalizeUsernameCompletion(sessionID, session, material, normalizedUsername, usernameHash, index, "Logged in as %s (%s). %s")
}

func finalizeUsernameCompletion(sessionID string, session *pendingSession, material accounts.Material, normalizedUsername, usernameHash string, index usernameIndex, messageFormat string) (ConnectResult, error) {
	result, err := finalizeCompletion(sessionID, session, material, usernameHash)
	if err != nil {
		return ConnectResult{}, err
	}
	index.add(usernameHash, material.AccountID)
	if err := saveUsernameIndex(index); err != nil {
		return ConnectResult{}, err
	}
	if _, err := saveLocalProfile(material.AccountID, normalizedUsername); err != nil {
		return ConnectResult{}, err
	}
	result.Message = fmt.Sprintf(messageFormat, normalizedUsername, material.AccountID, result.Message)
	return result, nil
}

func restoreRecoveryCompletion(sessionID string, session *pendingSession, password string) (ConnectResult, error) {
	filesByPath, err := recoveryBundleMap(session.response.RecoveryBundle)
	if err != nil {
		return ConnectResult{}, err
	}

	accountID := strings.TrimSpace(session.response.AccountID)
	if accountID == "" {
		return ConnectResult{}, errors.New("recovery bundle did not include an account id")
	}

	blobData, ok := filesByPath[accountBlobRelativePath(accountID)]
	if !ok {
		return ConnectResult{}, errors.New("recovery bundle is missing account blob data")
	}

	material, err := recoverAccount(blobData, password)
	if err != nil {
		return ConnectResult{}, err
	}
	if material.AccountID != accountID {
		return ConnectResult{}, errors.New("recovery bundle account id does not match recovered account")
	}

	peerData, metaData, accountPubKeyData, accountMetaData, err := validateRecoveryBundleFiles(session.nodeID, material.AccountID, material.PublicKey, filesByPath)
	if err != nil {
		return ConnectResult{}, err
	}

	peerPath, metaPath, accountBlobPath, err := writeNetworkFiles(
		session.nodeID,
		material.AccountID,
		peerData,
		metaData,
		blobData,
		accountPubKeyData,
		accountMetaData,
	)
	if err != nil {
		return ConnectResult{}, err
	}

	if usernameHash := strings.TrimSpace(session.response.UsernameHash); usernameHash != "" {
		index, err := loadUsernameIndex()
		if err != nil {
			return ConnectResult{}, err
		}
		index.add(usernameHash, material.AccountID)
		if err := saveUsernameIndex(index); err != nil {
			return ConnectResult{}, err
		}
	}

	removePendingSession(sessionID)

	result := completedConnectResult(session, material, peerPath, metaPath, accountBlobPath)
	result.Message = fmt.Sprintf("Recovered account %s and restored %s, %s, %s, and %s.", material.AccountID, material.LocalKeyPath, peerPath, metaPath, accountBlobPath)
	return result, nil
}

func finalizeCompletion(sessionID string, session *pendingSession, material accounts.Material, usernameHash string) (ConnectResult, error) {
	artifacts, err := buildCompletionArtifacts(session, material, usernameHash)
	if err != nil {
		return ConnectResult{}, err
	}

	peerPath, metaPath, accountBlobPath, err := writeNetworkFiles(
		session.nodeID,
		material.AccountID,
		artifacts.peerData,
		artifacts.metaData,
		material.BlobData,
		artifacts.accountPubKeyData,
		artifacts.accountMetaData,
	)
	if err != nil {
		return ConnectResult{}, err
	}

	if err := finalizeBootstrapSession(sessionID, session, material, artifacts); err != nil {
		return ConnectResult{}, err
	}

	return completedConnectResult(session, material, peerPath, metaPath, accountBlobPath), nil
}

func normalizeUsername(username string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(username))
	if normalized == "" {
		return "", errors.New("username is required")
	}

	return normalized, nil
}

func (index usernameIndex) accountIDsForHash(usernameHash string) []string {
	usernameHash = strings.TrimSpace(usernameHash)
	if usernameHash == "" {
		return nil
	}

	accountIDs := index[usernameHash]
	if len(accountIDs) == 0 {
		return nil
	}

	deduped := make([]string, 0, len(accountIDs))
	seen := map[string]struct{}{}
	for _, accountID := range accountIDs {
		accountID = strings.TrimSpace(accountID)
		if accountID == "" {
			continue
		}
		if _, exists := seen[accountID]; exists {
			continue
		}
		seen[accountID] = struct{}{}
		deduped = append(deduped, accountID)
	}

	return deduped
}

func (index usernameIndex) add(usernameHash, accountID string) {
	if index == nil {
		return
	}

	usernameHash = strings.TrimSpace(usernameHash)
	accountID = strings.TrimSpace(accountID)
	if usernameHash == "" || accountID == "" {
		return
	}

	for _, existing := range index[usernameHash] {
		if strings.TrimSpace(existing) == accountID {
			return
		}
	}

	index[usernameHash] = append(index[usernameHash], accountID)
}

func containsAccountID(accountIDs []string, accountID string) bool {
	accountID = strings.TrimSpace(accountID)
	for _, existing := range accountIDs {
		if strings.TrimSpace(existing) == accountID {
			return true
		}
	}

	return false
}

func recoveryBundleMap(bundle []recoveryBundleFile) (map[string][]byte, error) {
	if len(bundle) == 0 {
		return nil, errors.New("recovery bundle is missing")
	}

	filesByPath := make(map[string][]byte, len(bundle))
	for _, file := range bundle {
		path := strings.TrimSpace(file.Path)
		if path == "" {
			return nil, errors.New("recovery bundle contains an empty path")
		}
		if len(file.Data) == 0 {
			return nil, fmt.Errorf("recovery bundle file %s is empty", path)
		}
		filesByPath[path] = append([]byte(nil), file.Data...)
	}

	return filesByPath, nil
}

func loadUsernameIndexCache() (usernameIndex, error) {
	data, err := readManagedFile(usernameIndexPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return usernameIndex{}, nil
		}

		return nil, err
	}

	index := usernameIndex{}
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}

	return index, nil
}

func saveUsernameIndexCache(index usernameIndex) error {
	if index == nil {
		index = usernameIndex{}
	}

	data, err := marshalIndexJSON(index)
	if err != nil {
		return err
	}

	return writeManagedFile(usernameIndexPath, data, 0o644)
}

type completionArtifacts struct {
	peerData          []byte
	metaData          []byte
	accountPubKeyData []byte
	accountMetaData   []byte
}

type completionState struct {
	firstSeen        string
	revision         int
	accountCreatedAt string
	accountRevision  int
}

func validateCompletionInputs(sessionID, password string) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("bootstrap session id is required")
	}
	if strings.TrimSpace(password) == "" {
		return errors.New("bootstrap password is required")
	}

	return nil
}

func pendingSessionForCompletion(sessionID string) (*pendingSession, error) {
	session, ok := getPendingSession(sessionID)
	if !ok {
		return nil, errors.New("bootstrap session expired or was not found")
	}

	return session, nil
}

func completionMaterial(response bootstrapSessionStartResponse, password string) (accounts.Material, error) {
	if response.RecoveryAvailable {
		return recoverAccount(response.AccountBlob, password)
	}

	return createAccount(password)
}

func buildCompletionArtifacts(session *pendingSession, material accounts.Material, usernameHash string) (completionArtifacts, error) {
	state := normalizeCompletionState(session.response)
	if strings.TrimSpace(usernameHash) == "" {
		usernameHash = strings.TrimSpace(session.response.UsernameHash)
	}

	peerData, err := buildPeerFile(session.response.ObservedIPv4, session.response.Port, material.AccountID, material.PrivateKey)
	if err != nil {
		return completionArtifacts{}, err
	}
	metaData, err := buildMetaFile(session.nodeID, material.AccountID, state.firstSeen, state.revision, material.PrivateKey)
	if err != nil {
		return completionArtifacts{}, err
	}
	accountPubKeyData := accounts.BuildPublicKeyFile(material.PublicKey)
	accountMetaData, err := accounts.BuildMetaWithUsernameHash(material.AccountID, material.PublicKey, state.accountCreatedAt, state.accountRevision, usernameHash, material.PrivateKey)
	if err != nil {
		return completionArtifacts{}, err
	}

	return completionArtifacts{
		peerData:          peerData,
		metaData:          metaData,
		accountPubKeyData: accountPubKeyData,
		accountMetaData:   accountMetaData,
	}, nil
}

func normalizeCompletionState(response bootstrapSessionStartResponse) completionState {
	now := currentTime().UTC().Format(time.RFC3339)

	firstSeen := response.FirstSeen
	if strings.TrimSpace(firstSeen) == "" {
		firstSeen = now
	}
	revision := response.Revision
	if revision <= 0 {
		revision = 1
	}
	accountCreatedAt := response.AccountCreatedAt
	if strings.TrimSpace(accountCreatedAt) == "" {
		accountCreatedAt = now
	}
	accountRevision := response.AccountRevision
	if accountRevision <= 0 {
		accountRevision = 1
	}

	return completionState{
		firstSeen:        firstSeen,
		revision:         revision,
		accountCreatedAt: accountCreatedAt,
		accountRevision:  accountRevision,
	}
}

func finalizeBootstrapSession(sessionID string, session *pendingSession, material accounts.Material, artifacts completionArtifacts) error {
	finalizeRequest := bootstrapSessionFinalizeRequest{
		Type:          "finalize",
		NodeID:        session.nodeID,
		AccountID:     material.AccountID,
		PeerData:      artifacts.peerData,
		MetaData:      artifacts.metaData,
		AccountBlob:   material.BlobData,
		AccountPubKey: artifacts.accountPubKeyData,
		AccountMeta:   artifacts.accountMetaData,
	}
	if err := json.NewEncoder(session.conn).Encode(finalizeRequest); err != nil {
		removePendingSession(sessionID)
		return err
	}

	var finalizeResponse bootstrapSessionFinalizeResponse
	if err := json.NewDecoder(session.conn).Decode(&finalizeResponse); err != nil {
		removePendingSession(sessionID)
		return err
	}
	removePendingSession(sessionID)

	if finalizeResponse.Error != "" {
		return errors.New(finalizeResponse.Error)
	}

	return nil
}

func completedConnectResult(session *pendingSession, material accounts.Material, peerPath, metaPath, accountBlobPath string) ConnectResult {
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
		return result
	}

	result.Message = fmt.Sprintf("Bootstrap completed in degraded mode. Saved %s, %s, %s, and %s.", material.LocalKeyPath, peerPath, metaPath, accountBlobPath)
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}

	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}

	return 0
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

		go handleBootstrapConnection(wrapBootstrapConn(conn))
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

	existingRecords, err := loadNodeRecords(request.NodeID)
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
	if strings.TrimSpace(existingRecords.AccountMeta.UsernameHash) != "" {
		response.UsernameHash = existingRecords.AccountMeta.UsernameHash
	}
	if recoveryBundle := buildRecoveryBundle(request.NodeID, existingRecords); strings.TrimSpace(existingRecords.NodeMeta.AccountID) != "" && len(recoveryBundle) > 0 {
		response.RecoveryAvailable = true
		response.AccountID = existingRecords.NodeMeta.AccountID
		response.AccountBlob = existingRecords.AccountBlob
		response.RecoveryBundle = recoveryBundle
	}

	return response
}

func loadExistingNodeRecords(nodeID string) (existingNodeRecords, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return existingNodeRecords{}, nil
	}

	peerData, err := ensureKnownNodeExists(nodeID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return existingNodeRecords{}, nil
		}
		return existingNodeRecords{}, err
	}

	metaData, existingMeta, hasMeta, err := loadExistingNodeMeta(nodeID)
	if err != nil {
		return existingNodeRecords{}, err
	}
	if !hasMeta {
		return existingNodeRecords{KnownNode: true}, nil
	}

	records := existingNodeRecords{PeerData: peerData, NodeMetaData: metaData, NodeMeta: existingMeta, KnownNode: true}
	if strings.TrimSpace(existingMeta.AccountID) == "" {
		return records, nil
	}

	return loadExistingAccountRecords(nodeID, metaData, records)
}

func ensureKnownNodeExists(nodeID string) ([]byte, error) {
	return readManagedFile(peerRelativePath(nodeID))
}

func loadExistingNodeMeta(nodeID string) ([]byte, metaFile, bool, error) {
	metaData, err := readManagedFile(metaRelativePath(nodeID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, metaFile{}, false, nil
		}
		return nil, metaFile{}, false, err
	}

	var existingMeta metaFile
	if err := json.Unmarshal(metaData, &existingMeta); err != nil {
		return nil, metaFile{}, false, err
	}

	return metaData, existingMeta, true, nil
}

func loadExistingAccountRecords(nodeID string, metaData []byte, records existingNodeRecords) (existingNodeRecords, error) {
	accountPubKey, hasPubKey, err := loadExistingAccountPubKey(records.NodeMeta.AccountID)
	if err != nil {
		return existingNodeRecords{}, err
	}
	if !hasPubKey {
		return records, nil
	}
	records.AccountPubKey = accountPubKey
	accountPubKeyData, err := readManagedFile(accountPubKeyRelativePath(records.NodeMeta.AccountID))
	if err != nil {
		return existingNodeRecords{}, err
	}
	records.AccountPubKeyData = accountPubKeyData

	accountMeta, hasAccountMeta, err := loadExistingAccountMeta(records.NodeMeta.AccountID, accountPubKey)
	if err != nil {
		return existingNodeRecords{}, err
	}
	if !hasAccountMeta {
		return records, nil
	}
	records.AccountMeta = accountMeta
	accountMetaData, err := readManagedFile(accountMetaRelativePath(records.NodeMeta.AccountID))
	if err != nil {
		return existingNodeRecords{}, err
	}
	records.AccountMetaData = accountMetaData

	if err := verifyMetaFile(metaData, nodeID, records.NodeMeta.AccountID, accountPubKey); err != nil {
		return existingNodeRecords{}, err
	}

	accountBlob, hasBlob, err := loadExistingAccountBlob(records.NodeMeta.AccountID, accountPubKey)
	if err != nil {
		return existingNodeRecords{}, err
	}
	if hasBlob {
		records.AccountBlob = accountBlob
	}

	return records, nil
}

func buildRecoveryBundle(nodeID string, records existingNodeRecords) []recoveryBundleFile {
	accountID := strings.TrimSpace(records.NodeMeta.AccountID)
	if strings.TrimSpace(nodeID) == "" || accountID == "" {
		return nil
	}
	if len(records.PeerData) == 0 || len(records.NodeMetaData) == 0 || len(records.AccountBlob) == 0 || len(records.AccountPubKeyData) == 0 || len(records.AccountMetaData) == 0 {
		return nil
	}

	return []recoveryBundleFile{
		{Path: peerRelativePath(nodeID), Data: append([]byte(nil), records.PeerData...)},
		{Path: metaRelativePath(nodeID), Data: append([]byte(nil), records.NodeMetaData...)},
		{Path: accountPubKeyRelativePath(accountID), Data: append([]byte(nil), records.AccountPubKeyData...)},
		{Path: accountMetaRelativePath(accountID), Data: append([]byte(nil), records.AccountMetaData...)},
		{Path: accountBlobRelativePath(accountID), Data: append([]byte(nil), records.AccountBlob...)},
	}
}

func validateRecoveryBundleFiles(nodeID, accountID string, publicKey ed25519.PublicKey, filesByPath map[string][]byte) ([]byte, []byte, []byte, []byte, error) {
	peerPath := peerRelativePath(nodeID)
	metaPath := metaRelativePath(nodeID)
	accountPubKeyPath := accountPubKeyRelativePath(accountID)
	accountMetaPath := accountMetaRelativePath(accountID)
	accountBlobPath := accountBlobRelativePath(accountID)

	peerData, ok := filesByPath[peerPath]
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("recovery bundle is missing %s", peerPath)
	}
	metaData, ok := filesByPath[metaPath]
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("recovery bundle is missing %s", metaPath)
	}
	accountPubKeyData, ok := filesByPath[accountPubKeyPath]
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("recovery bundle is missing %s", accountPubKeyPath)
	}
	accountMetaData, ok := filesByPath[accountMetaPath]
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("recovery bundle is missing %s", accountMetaPath)
	}
	accountBlobData, ok := filesByPath[accountBlobPath]
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("recovery bundle is missing %s", accountBlobPath)
	}

	bundlePubKey, err := accounts.VerifyPublicKeyFile(accountID, accountPubKeyData)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if !bundlePubKey.Equal(publicKey) {
		return nil, nil, nil, nil, errors.New("recovery bundle public key does not match recovered account")
	}
	if _, err := accounts.VerifyMeta(accountID, bundlePubKey, accountMetaData); err != nil {
		return nil, nil, nil, nil, err
	}
	if _, blobPublicKey, err := accounts.ValidateBlob(accountBlobData); err != nil {
		return nil, nil, nil, nil, err
	} else if !ed25519.PublicKey(blobPublicKey).Equal(bundlePubKey) {
		return nil, nil, nil, nil, errors.New("recovery bundle account blob public key does not match trusted account pubkey")
	}
	if err := verifyPeerFile(peerData, accountID, bundlePubKey); err != nil {
		return nil, nil, nil, nil, err
	}
	if err := verifyMetaFile(metaData, nodeID, accountID, bundlePubKey); err != nil {
		return nil, nil, nil, nil, err
	}

	return peerData, metaData, accountPubKeyData, accountMetaData, nil
}

func loadExistingAccountPubKey(accountID string) (ed25519.PublicKey, bool, error) {
	accountPubKeyData, err := readManagedFile(accountPubKeyRelativePath(accountID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}

	accountPubKey, err := accounts.VerifyPublicKeyFile(accountID, accountPubKeyData)
	if err != nil {
		return nil, false, err
	}

	return accountPubKey, true, nil
}

func loadExistingAccountMeta(accountID string, accountPubKey ed25519.PublicKey) (accounts.Meta, bool, error) {
	accountMetaData, err := readManagedFile(accountMetaRelativePath(accountID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return accounts.Meta{}, false, nil
		}
		return accounts.Meta{}, false, err
	}

	accountMeta, err := accounts.VerifyMeta(accountID, accountPubKey, accountMetaData)
	if err != nil {
		return accounts.Meta{}, false, err
	}

	return accountMeta, true, nil
}

func loadExistingAccountBlob(accountID string, accountPubKey ed25519.PublicKey) ([]byte, bool, error) {
	blobData, err := readManagedFile(accountBlobRelativePath(accountID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}

	_, blobPublicKey, err := accounts.ValidateBlob(blobData)
	if err != nil {
		return nil, false, err
	}
	if !ed25519.PublicKey(blobPublicKey).Equal(accountPubKey) {
		return nil, false, errors.New("account blob public key does not match trusted account pubkey")
	}

	return blobData, true, nil
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
	signature, err := signPeerOrMeta(privateKey, unsigned)
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
	signature, err := signPeerOrMeta(privateKey, unsigned)
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

	existingRecords, err := loadNodeRecords(request.NodeID)
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
	err := verifySignedPayload(unsignedMetaFile{
		NodeID:    file.NodeID,
		AccountID: file.AccountID,
		FirstSeen: file.FirstSeen,
		Revision:  file.Revision,
		UpdatedAt: file.UpdatedAt,
	}, file.Signature, publicKey)
	if err == nil {
		return nil
	}

	if verifySignedPayload(legacyUnsignedMetaFile{
		NodeID:    file.NodeID,
		AccountID: file.AccountID,
		FirstSeen: file.FirstSeen,
		Revision:  file.Revision,
		UpdatedAt: file.UpdatedAt,
	}, file.Signature, publicKey) == nil {
		return nil
	}

	return err
}

func verifySignedPayload(payload any, signature string, publicKey ed25519.PublicKey) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if decodedSignature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature)); err != nil {
		return err
	} else if !ed25519.Verify(publicKey, data, decodedSignature) {
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
