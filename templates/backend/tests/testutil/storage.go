// Package testutil provides scaffolded database and Redis test helpers.
package testutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// MockStorage provides an in-memory storage implementation for testing.
// It implements common storage interface patterns used in Ovasabi applications.
type MockStorage struct {
	mu    sync.RWMutex
	Files map[string][]byte
}

// NewMockStorage creates a new mock storage instance.
func NewMockStorage() *MockStorage {
	return &MockStorage{
		Files: make(map[string][]byte),
	}
}

// Upload stores data in memory and returns a mock URL.
func (m *MockStorage) Upload(ctx context.Context, data io.Reader, filename, contentType string, metadata map[string]string) (string, error) {
	content, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("read upload data: %w", err)
	}

	fileID := fmt.Sprintf("mock_file_%d_%s", time.Now().UnixNano(), filename)

	m.mu.Lock()
	m.Files[fileID] = content
	m.mu.Unlock()

	return "http://mock-storage/" + fileID, nil
}

// Download retrieves data from memory by file ID.
func (m *MockStorage) Download(ctx context.Context, fileID string) (io.ReadCloser, error) {
	normalized := normalizeFileID(fileID)

	m.mu.RLock()
	content, ok := m.Files[normalized]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("mock storage file not found: %s", fileID)
	}

	return io.NopCloser(bytes.NewReader(content)), nil
}

// Delete removes a file from memory.
func (m *MockStorage) Delete(ctx context.Context, fileID string) error {
	normalized := normalizeFileID(fileID)

	m.mu.Lock()
	delete(m.Files, normalized)
	m.mu.Unlock()

	return nil
}

// Exists checks if a file exists in memory.
func (m *MockStorage) Exists(ctx context.Context, fileID string) (bool, error) {
	normalized := normalizeFileID(fileID)

	m.mu.RLock()
	_, ok := m.Files[normalized]
	m.mu.RUnlock()

	return ok, nil
}

// GetURL returns a mock URL for the file.
func (m *MockStorage) GetURL(ctx context.Context, fileID string) (string, error) {
	return "http://mock-storage/" + fileID, nil
}

// Clear removes all files from storage.
func (m *MockStorage) Clear() {
	m.mu.Lock()
	m.Files = make(map[string][]byte)
	m.mu.Unlock()
}

// Count returns the number of stored files.
func (m *MockStorage) Count() int {
	m.mu.RLock()
	count := len(m.Files)
	m.mu.RUnlock()
	return count
}

func normalizeFileID(fileID string) string {
	normalized := fileID
	if slash := strings.LastIndex(normalized, "/"); slash >= 0 && slash < len(normalized)-1 {
		normalized = normalized[slash+1:]
	}
	if q := strings.Index(normalized, "?"); q >= 0 {
		normalized = normalized[:q]
	}
	return normalized
}
