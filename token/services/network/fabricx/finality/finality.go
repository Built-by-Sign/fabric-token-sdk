/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package finality

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hyperledger-labs/fabric-smart-client/pkg/utils/errors"
	cdriver "github.com/hyperledger-labs/fabric-smart-client/platform/common/driver"
	fdriver "github.com/hyperledger-labs/fabric-smart-client/platform/fabric/driver"
	"github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/committer/queryservice"
	finalityx "github.com/hyperledger-labs/fabric-smart-client/platform/fabricx/core/finality"
	"github.com/hyperledger-labs/fabric-token-sdk/token/services/logging"
	"github.com/hyperledger-labs/fabric-token-sdk/token/services/network/common/rws/keys"
	"github.com/hyperledger-labs/fabric-token-sdk/token/services/network/common/rws/translator"
	ndriver "github.com/hyperledger-labs/fabric-token-sdk/token/services/network/driver"
	"github.com/hyperledger-labs/fabric-token-sdk/token/services/network/fabric/finality"
	"github.com/hyperledger-labs/fabric-token-sdk/token/services/network/fabricx/finality/queue"
	"github.com/hyperledger/fabric-x-common/api/committerpb"
)

var logger = logging.MustGetLogger()

const (
	// defaultMaxRetries is the number of times a ListenerEvent will retry on transient errors.
	defaultMaxRetries = 3
	// defaultRetryInterval is the initial backoff delay; it doubles after each attempt.
	defaultRetryInterval = time.Second

	// defaultPollInterval is how often the shared poller sweeps the pending set.
	defaultPollInterval = time.Second
	// defaultPollBatchSize bounds how many txIDs go into one committer status query.
	defaultPollBatchSize = 2000
	// defaultPendingTTL bounds how long a tx stays pending before its slot is
	// reclaimed. It must exceed the longest caller finality timeout so the poller
	// keeps checking for as long as any watcher waits.
	defaultPendingTTL = 10 * time.Minute
)

// ConfigService models the configuration service needed by the NSListenerManager
//
//go:generate counterfeiter -o mock/cs.go -fake-name ConfigService . ConfigService
type ConfigService interface {
	// UnmarshalKey unmarshals the configuration value for the given key into rawVal
	UnmarshalKey(key string, rawVal any) error
}

// QueryService models the FabricX query service needed by the NSListenerManager
//
//go:generate counterfeiter -o mock/qs.go -fake-name QueryService . QueryService
type QueryService interface {
	// GetState returns the value for the given namespace and key
	GetState(ns cdriver.Namespace, key cdriver.PKey) (*cdriver.VaultValue, error)
	// GetStates returns the values for the given namespaces and keys
	GetStates(map[cdriver.Namespace][]cdriver.PKey) (map[cdriver.Namespace]map[cdriver.PKey]cdriver.VaultValue, error)
	// GetTransactionStatus returns the status of the given transaction
	GetTransactionStatus(txID string) (int32, error)
	GetConfigTransaction() (*queryservice.ConfigTransactionInfo, error)
}

// Listener is an alias for ndriver.FinalityListener
//
//go:generate counterfeiter -o mock/fl.go -fake-name Listener . Listener
type Listener = ndriver.FinalityListener

// Queue models an event processor
//
//go:generate counterfeiter -o mock/queue.go -fake-name Queue . Queue
type Queue interface {
	// EnqueueBlocking adds an event to the queue and blocks until it is accepted or the context is canceled
	EnqueueBlocking(ctx context.Context, event queue.Event) error
	// Enqueue adds an event to the queue and returns immediately
	Enqueue(event queue.Event) (err error)
}

// KeyTranslator is an alias for translator.KeyTranslator
//
//go:generate counterfeiter -o mock/kt.go -fake-name KeyTranslator . KeyTranslator
type KeyTranslator = translator.KeyTranslator

// QueryServiceProvider is an alias for queryservice.Provider
//
//go:generate counterfeiter -o mock/qps.go -fake-name QueryServiceProvider . QueryServiceProvider
type QueryServiceProvider = queryservice.Provider

// ListenerManager is an alias for finalityx.ListenerManager
//
//go:generate counterfeiter -o mock/lm.go -fake-name ListenerManager . ListenerManager
type ListenerManager = finalityx.ListenerManager

// ListenerManagerProvider gives access to instances of ListenerManager
//
//go:generate counterfeiter -o mock/fp.go -fake-name ListenerManagerProvider . ListenerManagerProvider
type ListenerManagerProvider interface {
	NewManager(network, channel string) (ListenerManager, error)
}

// ListenerEvent represents a finality event notification
type ListenerEvent struct {
	// QueryService is the service used to query the state of the network
	QueryService QueryService
	// KeyTranslator is the service used to translate keys
	KeyTranslator KeyTranslator

	// Listener is the listener to be notified
	Listener Listener
	// TxID is the transaction ID
	TxID string
	// Status is the status of the transaction
	Status fdriver.ValidationCode
	// StatusMessage is the status message
	StatusMessage string
	// Namespace is the namespace of the transaction
	Namespace string

	// MaxRetries is the number of retry attempts on transient errors (0 uses defaultMaxRetries).
	MaxRetries int
	// RetryInterval is the initial backoff delay between retries, doubling each attempt (0 uses defaultRetryInterval).
	RetryInterval time.Duration
}

// Process handles a finality event notification with exponential-backoff retries.
// If the status is Unknown or Busy, it triggers a manual transaction check.
// If the status is Valid, it retrieves the token request hash from the ledger.
// It notifies the wrapped listener on success, or calls OnError if all retries are exhausted.
func (l *ListenerEvent) Process(ctx context.Context) error {
	maxRetries := l.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	retryInterval := l.RetryInterval
	if retryInterval <= 0 {
		retryInterval = defaultRetryInterval
	}

	delay := retryInterval
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := l.process(ctx)
		if err == nil {
			return nil
		}
		if attempt == maxRetries {
			logger.Errorf("[ListenerEvent] tx [%s] failed after %d attempts: %v — notifying listener", l.TxID, maxRetries+1, err)
			l.Listener.OnError(ctx, l.TxID, err)

			return nil
		}
		logger.Warnf("[ListenerEvent] tx [%s] attempt %d/%d failed: %v, retrying in %v", l.TxID, attempt+1, maxRetries+1, err, delay)
		select {
		case <-time.After(delay):
			delay *= 2
		case <-ctx.Done():
			logger.Warnf("[ListenerEvent] tx [%s] context canceled during retry backoff", l.TxID)

			return nil
		}
	}

	return nil
}

// process executes a single attempt at handling the finality event.
func (l *ListenerEvent) process(ctx context.Context) error {
	logger.Debugf("[ListenerEvent] get notification for [%s], status [%d]", l.TxID, l.Status)

	if l.Status == fdriver.Unknown || l.Status == fdriver.Busy {
		txCheck := TxCheck{
			QueryService:  l.QueryService,
			KeyTranslator: l.KeyTranslator,
			Listener:      l.Listener,
			TxID:          l.TxID,
			Namespace:     l.Namespace,
		}
		if err := txCheck.Process(ctx); err == nil {
			return nil
		}
	}

	var tokenRequestHash []byte
	if l.Status == fdriver.Valid {
		key, err := l.KeyTranslator.CreateTokenRequestKey(l.TxID)
		if err != nil {
			return errors.Wrapf(err, "can't create for token request [%s]", l.TxID)
		}
		v, err := l.QueryService.GetState(l.Namespace, key)
		if err != nil {
			return errors.Wrapf(err, "can't get state for token request [%s]", l.TxID)
		}
		tokenRequestHash = v.Raw
	}
	l.Listener.OnStatus(ctx, l.TxID, l.Status, l.StatusMessage, tokenRequestHash)

	return nil
}

// String returns a string representation of the event.
func (l *ListenerEvent) String() string {
	return fmt.Sprintf("ListenerEvent[%s]", l.TxID)
}

// TxCheck represents a transaction check event
type TxCheck struct {
	// QueryService is the service used to query the state of the network
	QueryService QueryService
	// KeyTranslator is the service used to translate keys
	KeyTranslator KeyTranslator

	// Listener is the listener to be notified
	Listener Listener
	// TxID is the transaction ID
	TxID string
	// Namespace is the namespace of the transaction
	Namespace string
}

// Process executes the transaction check by querying the current status of
// a transaction from the query service. If the transaction is in a known
// state (Valid or Invalid), it notifies the listener. If it's still
// processing (Unknown or Busy), it returns an error.
func (t *TxCheck) Process(ctx context.Context) error {
	logger.Debugf("[TxCheck] check for transaction [%s]", t.TxID)

	var err error
	s, err := t.QueryService.GetTransactionStatus(t.TxID)
	if err != nil {
		return errors.Wrapf(err, "can't get status for tx [%s]", t.TxID)
	}
	status := fabricXFSCStatus(s)

	logger.Debugf("check for transaction [%s], status [%d]", t.TxID, status)
	if status == fdriver.Unknown || status == fdriver.Busy {
		return errors.Errorf("transaction [%s] is not in a valid state", t.TxID)
	}

	var tokenRequestHash []byte
	if status == fdriver.Valid {
		// fetch token request hash key
		key, err := t.KeyTranslator.CreateTokenRequestKey(t.TxID)
		if err != nil {
			return errors.Wrapf(err, "can't create for token request [%s]", t.TxID)
		}
		v, err := t.QueryService.GetState(t.Namespace, key)
		if err != nil {
			return errors.Wrapf(err, "can't get state for token request [%s]", t.TxID)
		}
		tokenRequestHash = v.Raw
	}
	logger.Debugf("check for transaction [%s], notify validity", t.TxID)

	t.Listener.OnStatus(ctx, t.TxID, status, "", tokenRequestHash)

	return nil
}

// String returns a string representation of the event.
func (t *TxCheck) String() string {
	return fmt.Sprintf("TxCheck[%s]", t.TxID)
}

// NSFinalityListener is a finality listener that uses a queue to process events asynchronously.
type NSFinalityListener struct {
	namespace     string
	listener      Listener
	queue         Queue
	queryService  QueryService
	keyTranslator KeyTranslator
}

// NewNSFinalityListener creates a new NSFinalityListener for the given namespace
// and listener, using the specified queue for asynchronous processing.
func NewNSFinalityListener(
	namespace string,
	listener Listener,
	queue Queue,
	qs QueryService,
	kt KeyTranslator,
) *NSFinalityListener {
	return &NSFinalityListener{
		namespace:     namespace,
		listener:      listener,
		queue:         queue,
		queryService:  qs,
		keyTranslator: kt,
	}
}

// OnStatus enqueues a ListenerEvent for the transaction ID and status
// to be processed asynchronously by the worker pool.
func (l *NSFinalityListener) OnStatus(ctx context.Context, txID cdriver.TxID, status fdriver.ValidationCode, statusMessage string) {
	// processing the event must be fast
	// we enqueue an event to be processed asynchronously
	if err := l.queue.EnqueueBlocking(ctx, &ListenerEvent{
		QueryService:  l.queryService,
		KeyTranslator: l.keyTranslator,
		Namespace:     l.namespace,
		Listener:      l.listener,
		TxID:          txID,
		Status:        status,
		StatusMessage: statusMessage,
	}); err != nil {
		logger.Errorf("failed processing event: %s", err)
	}
}

// NSListenerManager resolves transaction finality with a single shared poller
// that batches committer status queries across all pending transactions, rather
// than one remote query per transaction.
type NSListenerManager struct {
	lm            finalityx.ListenerManager // retained for wiring; status delivery now goes through the poller
	queue         Queue
	queryService  QueryService
	keyTranslator KeyTranslator

	pollInterval time.Duration
	batchSize    int
	pendingTTL   time.Duration

	mu        sync.Mutex
	pending   map[string]*pendingTx
	startOnce sync.Once
}

// pendingTx is a transaction awaiting finality, tracked by the shared poller.
type pendingTx struct {
	namespace    string
	listener     Listener
	registeredAt time.Time
}

// NewNSListenerManager creates a new NSListenerManager wrapping an underlying
// listener manager and utilizing an event queue.
func NewNSListenerManager(
	lm finalityx.ListenerManager,
	queue Queue,
	qs QueryService,
	keyTranslator KeyTranslator,
) *NSListenerManager {
	return &NSListenerManager{
		lm:            lm,
		queue:         queue,
		queryService:  qs,
		keyTranslator: keyTranslator,
		pollInterval:  defaultPollInterval,
		batchSize:     defaultPollBatchSize,
		pendingTTL:    defaultPendingTTL,
		pending:       make(map[string]*pendingTx),
	}
}

// AddFinalityListener registers a listener for the given transaction. The tx is
// added to a pending set that a single shared background poller sweeps in
// batches (see pollLoop): one committer status query covers all pending txs per
// chunk, and terminal txs are resolved from the result. This replaces the former
// per-tx remote check — a speculative GetTransactionStatus at registration time
// (which almost always saw an as-yet-uncommitted tx) plus a per-tx fallback —
// that serialized every worker on its own round-trip. The listener is wrapped in
// an OnlyOnceListener so it fires exactly once.
func (n *NSListenerManager) AddFinalityListener(namespace string, txID string, listener Listener) error {
	logger.Debugf("AddFinalityListener [%s]", txID)
	l := &OnlyOnceListener{listener: listener}

	n.mu.Lock()
	n.pending[txID] = &pendingTx{namespace: namespace, listener: l, registeredAt: time.Now()}
	n.mu.Unlock()

	n.startOnce.Do(func() { go n.pollLoop(context.Background()) })

	return nil
}

// batchStatusQuerier is implemented by query services that resolve many
// transaction statuses in a single round-trip. The poller uses it when available
// (the FabricX RemoteQueryService does) and otherwise falls back to per-tx
// GetTransactionStatus (e.g. tests with a mock query service).
type batchStatusQuerier interface {
	GetTransactionStatuses(txIDs []string) (map[string]int32, error)
}

// pollLoop sweeps the pending set every pollInterval until ctx is canceled. It is
// started once, lazily, on the first AddFinalityListener.
func (n *NSListenerManager) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(n.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.pollOnce(ctx)
		}
	}
}

// pollOnce snapshots the pending txIDs (reclaiming stale slots), queries their
// status in batches, and resolves the terminal ones.
func (n *NSListenerManager) pollOnce(ctx context.Context) {
	now := time.Now()
	n.mu.Lock()
	ids := make([]string, 0, len(n.pending))
	for txID, p := range n.pending {
		if now.Sub(p.registeredAt) > n.pendingTTL {
			// The caller's own finality timeout settles this tx; we only reclaim
			// the slot so a permanently-dropped tx can't grow the pending set.
			delete(n.pending, txID)
			continue
		}
		ids = append(ids, txID)
	}
	n.mu.Unlock()
	if len(ids) == 0 {
		return
	}

	for _, chunk := range chunkTxIDs(ids, n.batchSize) {
		statuses, err := n.getStatuses(chunk)
		if err != nil {
			logger.Warnf("finality poller: status query for %d txs failed: %v", len(chunk), err)
			continue
		}
		n.resolveBatch(ctx, statuses)
	}
}

// getStatuses resolves many statuses in one round-trip when the query service
// supports it, otherwise falls back to one call per tx.
func (n *NSListenerManager) getStatuses(txIDs []string) (map[string]int32, error) {
	if bq, ok := n.queryService.(batchStatusQuerier); ok {
		return bq.GetTransactionStatuses(txIDs)
	}
	out := make(map[string]int32, len(txIDs))
	for _, txID := range txIDs {
		s, err := n.queryService.GetTransactionStatus(txID)
		if err != nil {
			return nil, err
		}
		out[txID] = s
	}
	return out, nil
}

// resolveBatch handles the terminal transactions in a status response: it
// batch-fetches the token-request hash for the valid ones, then hands each off to
// the worker pool for notification (no network I/O on that path). Non-terminal
// (Unknown/Busy) txs are left pending for the next sweep.
func (n *NSListenerManager) resolveBatch(ctx context.Context, statuses map[string]int32) {
	type terminalTx struct {
		txID   string
		status fdriver.ValidationCode
		p      *pendingTx
	}

	var terminals []terminalTx
	n.mu.Lock()
	for txID, raw := range statuses {
		p, ok := n.pending[txID]
		if !ok {
			continue
		}
		status := fabricXFSCStatus(raw)
		if status == fdriver.Unknown || status == fdriver.Busy {
			continue
		}
		terminals = append(terminals, terminalTx{txID: txID, status: status, p: p})
	}
	n.mu.Unlock()
	if len(terminals) == 0 {
		return
	}

	// Batch the token-request-hash lookups for valid txs, grouped by namespace.
	hashQuery := map[cdriver.Namespace][]cdriver.PKey{}
	keyToTx := make(map[string]string, len(terminals))
	for _, t := range terminals {
		if t.status != fdriver.Valid {
			continue
		}
		key, err := n.keyTranslator.CreateTokenRequestKey(t.txID)
		if err != nil {
			logger.Warnf("finality poller: token request key for [%s]: %v", t.txID, err)
			continue
		}
		hashQuery[t.p.namespace] = append(hashQuery[t.p.namespace], key)
		keyToTx[key] = t.txID
	}

	hashes := make(map[string][]byte, len(keyToTx))
	if len(hashQuery) > 0 {
		states, err := n.queryService.GetStates(hashQuery)
		if err != nil {
			// Keep terminals pending and retry next sweep rather than notify without a hash.
			logger.Warnf("finality poller: token-request-hash batch query failed: %v", err)
			return
		}
		for _, byKey := range states {
			for key, v := range byKey {
				if txID, ok := keyToTx[key]; ok {
					hashes[txID] = v.Raw
				}
			}
		}
	}

	for _, t := range terminals {
		var hash []byte
		if t.status == fdriver.Valid {
			hash = hashes[t.txID]
		}
		ev := &resolveEvent{listener: t.p.listener, txID: t.txID, status: t.status, tokenRequestHash: hash}
		if err := n.queue.Enqueue(ev); err != nil {
			// Queue full: leave pending so the next sweep retries.
			logger.Warnf("finality poller: enqueue resolve for [%s] failed: %v", t.txID, err)
			continue
		}
		n.mu.Lock()
		// Delete only if the entry is still ours: a re-registration for the
		// same txID may have replaced it between the snapshot and now.
		if cur, ok := n.pending[t.txID]; ok && cur == t.p {
			delete(n.pending, t.txID)
		}
		n.mu.Unlock()
	}
}

// resolveEvent notifies a listener of an already-determined terminal status. It
// does no network I/O (status and token-request hash are precomputed by the
// poller), so the worker pool drains these quickly.
type resolveEvent struct {
	listener         Listener
	txID             string
	status           fdriver.ValidationCode
	tokenRequestHash []byte
}

func (e *resolveEvent) Process(ctx context.Context) error {
	e.listener.OnStatus(ctx, e.txID, e.status, "", e.tokenRequestHash)
	return nil
}

func (e *resolveEvent) String() string {
	return fmt.Sprintf("resolveEvent[%s]", e.txID)
}

// chunkTxIDs splits ids into slices of at most size elements.
func chunkTxIDs(ids []string, size int) [][]string {
	if size <= 0 {
		return [][]string{ids}
	}
	chunks := make([][]string, 0, (len(ids)+size-1)/size)
	for i := 0; i < len(ids); i += size {
		end := i + size
		if end > len(ids) {
			end = len(ids)
		}
		chunks = append(chunks, ids[i:end])
	}
	return chunks
}

// NSListenerManagerProvider is a provider for creating NSListenerManager instances.
type NSListenerManagerProvider struct {
	QueryServiceProvider    QueryServiceProvider
	ListenerManagerProvider ListenerManagerProvider
	queue                   Queue
}

// NewNotificationServiceBased creates a provider for NSListenerManager
// that relies on a query service and an event queue.
func NewNotificationServiceBased(
	queryServiceProvider QueryServiceProvider,
	listenerManagerProvider ListenerManagerProvider,
	queue Queue,
) finality.ListenerManagerProvider {
	return &NSListenerManagerProvider{
		QueryServiceProvider:    queryServiceProvider,
		ListenerManagerProvider: listenerManagerProvider,
		queue:                   queue,
	}
}

// NewManager returns a new NSListenerManager for the specified network and channel.
// It initializes the underlying listener manager and retrieves the query service.
func (n *NSListenerManagerProvider) NewManager(network, channel string) (finality.ListenerManager, error) {
	finalityManager, err := n.ListenerManagerProvider.NewManager(network, channel)
	if err != nil {
		return nil, errors.Wrapf(err, "failed creating finality manager")
	}

	qs, err := n.QueryServiceProvider.Get(network, channel)
	if err != nil {
		return nil, errors.Wrapf(err, "failed getting query service")
	}

	return NewNSListenerManager(finalityManager, n.queue, qs, &keys.Translator{}), nil
}

// OnlyOnceListener ensures that the wrapped finality listener is notified
// exactly once, regardless of how many times its OnStatus method is called.
type OnlyOnceListener struct {
	listener Listener
	once     sync.Once
}

// OnStatus notifies the wrapped listener only if it hasn't been notified before.
func (o *OnlyOnceListener) OnStatus(ctx context.Context, txID string, status int, message string, tokenRequestHash []byte) {
	o.once.Do(func() {
		o.listener.OnStatus(ctx, txID, status, message, tokenRequestHash)
	})
}

// OnError forwards the error to the wrapped listener only if it hasn't been notified before.
func (o *OnlyOnceListener) OnError(ctx context.Context, txID string, err error) {
	o.once.Do(func() {
		o.listener.OnError(ctx, txID, err)
	})
}

// fabricXFSCStatus maps Fabric-X transaction status codes to FSC validation codes.
func fabricXFSCStatus(c int32) fdriver.ValidationCode {
	switch committerpb.Status(c) {
	case committerpb.Status_STATUS_UNSPECIFIED:
		return fdriver.Unknown
	case committerpb.Status_COMMITTED:
		return fdriver.Valid
	default:
		return fdriver.Invalid
	}
}
