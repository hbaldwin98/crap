package mcpserver

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hbaldwin98/crap/internal/analysis"
)

const (
	defaultSnapshotTTL   = 30 * time.Minute
	defaultSnapshotCount = 8
	defaultSnapshotBytes = 64 << 20
	maximumSnapshotBytes = 16 << 20
)

type snapshot struct {
	id        string
	expiresAt time.Time
	data      []byte
}

type snapshotStore struct {
	mu         sync.Mutex
	snapshots  map[string]*snapshot
	order      []string
	totalBytes int
	ttl        time.Duration
	maxCount   int
	maxBytes   int
	maxEntry   int
	now        func() time.Time
	key        []byte
	reads      chan struct{}
}

func newSnapshotStore() *snapshotStore {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(fmt.Sprintf("create analysis snapshot cursor key: %v", err))
	}
	return &snapshotStore{snapshots: make(map[string]*snapshot), ttl: defaultSnapshotTTL, maxCount: defaultSnapshotCount, maxBytes: defaultSnapshotBytes, maxEntry: maximumSnapshotBytes, now: time.Now, key: key, reads: make(chan struct{}, 2)}
}

func (store *snapshotStore) put(report analysis.Report) (*snapshot, error) {
	return store.putContext(context.Background(), report)
}

func (store *snapshotStore) putContext(ctx context.Context, report analysis.Report) (*snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("serialize analysis snapshot: %w", err)
	}
	if len(data) > store.maxEntry {
		return nil, fmt.Errorf("analysis report is too large to retain (%d bytes; maximum %d)", len(data), store.maxEntry)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, fmt.Errorf("create analysis report ID: %w", err)
	}
	item := &snapshot{id: hex.EncodeToString(idBytes), expiresAt: store.now().Add(store.ttl), data: append([]byte(nil), data...)}
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	store.purgeExpiredLocked()
	for len(store.order) >= store.maxCount || store.totalBytes+len(data) > store.maxBytes {
		if len(store.order) == 0 {
			return nil, fmt.Errorf("analysis report exceeds snapshot storage capacity")
		}
		store.removeLocked(store.order[0])
	}
	store.snapshots[item.id] = item
	store.order = append(store.order, item.id)
	store.totalBytes += len(item.data)
	return item, nil
}

func (store *snapshotStore) get(id string) (*snapshot, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.purgeExpiredLocked()
	item := store.snapshots[id]
	if item == nil {
		return nil, fmt.Errorf("analysis report not found or expired; run analysis again")
	}
	return item, nil
}

func (store *snapshotStore) purgeExpiredLocked() {
	now := store.now()
	for _, id := range append([]string(nil), store.order...) {
		if item := store.snapshots[id]; item != nil && !now.Before(item.expiresAt) {
			store.removeLocked(id)
		}
	}
}

func (store *snapshotStore) removeLocked(id string) {
	item := store.snapshots[id]
	if item == nil {
		return
	}
	delete(store.snapshots, id)
	store.totalBytes -= len(item.data)
	for index, orderedID := range store.order {
		if orderedID == id {
			store.order = append(store.order[:index], store.order[index+1:]...)
			break
		}
	}
}

func (store *snapshotStore) decode(item *snapshot) (analysis.Report, error) {
	var report analysis.Report
	if err := json.Unmarshal(item.data, &report); err != nil {
		return analysis.Report{}, fmt.Errorf("decode analysis snapshot: %w", err)
	}
	return report, nil
}

type cursorState struct {
	Version    int    `json:"v"`
	ReportID   string `json:"reportId"`
	Offset     int    `json:"offset"`
	ResultMode string `json:"resultMode"`
	Limit      int    `json:"limit"`
}

func (store *snapshotStore) encodeCursor(cursor cursorState) string {
	data, _ := json.Marshal(cursor)
	signature := hmac.New(sha256.New, store.key)
	_, _ = signature.Write(data)
	return base64.RawURLEncoding.EncodeToString(append(data, signature.Sum(nil)...))
}

func (store *snapshotStore) decodeCursor(encoded string) (cursorState, error) {
	signed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(signed) <= sha256.Size {
		return cursorState{}, fmt.Errorf("invalid analysis cursor")
	}
	if base64.RawURLEncoding.EncodeToString(signed) != encoded {
		return cursorState{}, fmt.Errorf("invalid analysis cursor")
	}
	data, supplied := signed[:len(signed)-sha256.Size], signed[len(signed)-sha256.Size:]
	signature := hmac.New(sha256.New, store.key)
	_, _ = signature.Write(data)
	if !hmac.Equal(supplied, signature.Sum(nil)) {
		return cursorState{}, fmt.Errorf("invalid analysis cursor")
	}
	var cursor cursorState
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.Version != 1 || cursor.ReportID == "" || cursor.Offset < 0 {
		return cursorState{}, fmt.Errorf("invalid analysis cursor")
	}
	return cursor, nil
}
