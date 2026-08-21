package mutationmcp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/hbaldwin98/crap/internal/mutation"
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
		panic(fmt.Sprintf("create snapshot cursor key: %v", err))
	}
	return &snapshotStore{snapshots: make(map[string]*snapshot), ttl: defaultSnapshotTTL, maxCount: defaultSnapshotCount, maxBytes: defaultSnapshotBytes, maxEntry: maximumSnapshotBytes, now: time.Now, key: key, reads: make(chan struct{}, 2)}
}

func (store *snapshotStore) put(report mutation.Report) (*snapshot, error) {
	data, err := json.Marshal(report)
	if err != nil {
		return nil, fmt.Errorf("serialize mutation snapshot: %w", err)
	}
	if len(data) > store.maxEntry {
		return nil, fmt.Errorf("mutation report is too large to retain (%d bytes; maximum %d)", len(data), store.maxEntry)
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, fmt.Errorf("create mutation report ID: %w", err)
	}
	item := &snapshot{id: hex.EncodeToString(idBytes), expiresAt: store.now().Add(store.ttl), data: append([]byte(nil), data...)}
	store.mu.Lock()
	defer store.mu.Unlock()
	store.purgeExpiredLocked()
	for len(store.order) >= store.maxCount || store.totalBytes+len(data) > store.maxBytes {
		if len(store.order) == 0 {
			return nil, fmt.Errorf("mutation report exceeds snapshot storage capacity")
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
		return nil, fmt.Errorf("mutation report not found or expired; run mutation tests again")
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

func (store *snapshotStore) decode(item *snapshot) (mutation.Report, error) {
	var report mutation.Report
	if err := json.Unmarshal(item.data, &report); err != nil {
		return mutation.Report{}, fmt.Errorf("decode mutation snapshot: %w", err)
	}
	return report, nil
}

type cursorState struct {
	Version    int      `json:"v"`
	ReportID   string   `json:"reportId"`
	Offset     int      `json:"offset"`
	ResultMode string   `json:"resultMode"`
	Statuses   []string `json:"statuses"`
	Limit      int      `json:"limit"`
}

func (store *snapshotStore) encodeCursor(cursor cursorState) string {
	data, _ := json.Marshal(cursor)
	signature := hmac.New(sha256.New, store.key)
	_, _ = signature.Write(data)
	signed := append(data, signature.Sum(nil)...)
	return base64.RawURLEncoding.EncodeToString(signed)
}

func (store *snapshotStore) decodeCursor(encoded string) (cursorState, error) {
	signed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(signed) <= sha256.Size {
		return cursorState{}, fmt.Errorf("invalid mutation cursor")
	}
	data, supplied := signed[:len(signed)-sha256.Size], signed[len(signed)-sha256.Size:]
	signature := hmac.New(sha256.New, store.key)
	_, _ = signature.Write(data)
	if !hmac.Equal(supplied, signature.Sum(nil)) {
		return cursorState{}, fmt.Errorf("invalid mutation cursor")
	}
	var cursor cursorState
	if err := json.Unmarshal(data, &cursor); err != nil || cursor.Version != 1 || cursor.ReportID == "" || cursor.Offset < 0 {
		return cursorState{}, fmt.Errorf("invalid mutation cursor")
	}
	return cursor, nil
}
