package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

const (
	// DefaultRetentionDuration is 48 hours for P2P message retention.
	// Audit KT §7: reduced from 7 days for data minimization (legal risk).
	// 48h covers 99%+ of audit/third-verification windows.
	DefaultRetentionDuration = 48 * time.Hour

	// CleanupInterval controls how often expired entries are pruned.
	CleanupInterval = 1 * time.Hour
)

// RecordType identifies the kind of stored P2P record.
type RecordType string

const (
	RecordPrompt  RecordType = "prompt"
	RecordOutput  RecordType = "output"
	RecordReceipt RecordType = "receipt"
	RecordVerify  RecordType = "verify"
)

// Record is a single stored P2P message with TTL metadata.
type Record struct {
	Type      RecordType `json:"type"`
	TaskId    string     `json:"task_id"`
	Data      []byte     `json:"data"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
}

// Store provides persistent storage for P2P messages with automatic TTL-based expiry (P3-1).
type Store struct {
	dir       string
	retention time.Duration
	stopCh    chan struct{}
}

// New creates a new persistent store at the given directory.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Store{
		dir:       dir,
		retention: DefaultRetentionDuration,
		stopCh:    make(chan struct{}),
	}
	go s.cleanupLoop()
	return s, nil
}

// Put stores a record with automatic TTL.
func (s *Store) Put(recType RecordType, taskId []byte, data []byte) error {
	now := time.Now()
	rec := Record{
		Type:      recType,
		TaskId:    hex.EncodeToString(taskId),
		Data:      data,
		CreatedAt: now,
		ExpiresAt: now.Add(s.retention),
	}
	bz, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	key := s.recordKey(recType, taskId)
	path := filepath.Join(s.dir, key)
	return os.WriteFile(path, bz, 0o644)
}

// Get retrieves a record if it exists and hasn't expired.
func (s *Store) Get(recType RecordType, taskId []byte) (*Record, error) {
	key := s.recordKey(recType, taskId)
	path := filepath.Join(s.dir, key)
	bz, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var rec Record
	if err := json.Unmarshal(bz, &rec); err != nil {
		return nil, err
	}
	if time.Now().After(rec.ExpiresAt) {
		os.Remove(path)
		return nil, nil
	}
	return &rec, nil
}

// recordKey generates a filename from record type and task ID.
func (s *Store) recordKey(recType RecordType, taskId []byte) string {
	h := sha256.Sum256(append([]byte(recType), taskId...))
	return hex.EncodeToString(h[:16]) + ".json"
}

// cleanupLoop periodically removes expired records.
func (s *Store) cleanupLoop() {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.cleanup()
		}
	}
}

func (s *Store) cleanup() {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		bz, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec Record
		if err := json.Unmarshal(bz, &rec); err != nil {
			continue
		}
		if now.After(rec.ExpiresAt) {
			os.Remove(path)
		}
	}
}

// Close stops the cleanup goroutine.
func (s *Store) Close() {
	close(s.stopCh)
}
