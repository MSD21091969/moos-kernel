package kernel

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"moos/kernel/internal/graph"
)

const maxLogLineBytes = 10 * 1024 * 1024 // 10 MB max per log line

// LogStore is a JSONL (newline-delimited JSON) append-only Store.
// Each line is one PersistedRewrite. The file is opened in O_APPEND mode.
// Safe for single-process use; not intended for multi-process sharing.
type LogStore struct {
	mu   sync.Mutex
	path string
}

func NewLogStore(path string) (*LogStore, error) {
	// Create or verify the file is writable
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("log_store: open %q: %w", path, err)
	}
	f.Close()
	return &LogStore{path: path}, nil
}

func (l *LogStore) Append(entries []graph.PersistedRewrite) error {
	if len(entries) == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("log_store: open for append: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("log_store: marshal entry seq %d: %w", entry.LogSeq, err)
		}
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("log_store: write: %w", err)
		}
		if err := w.WriteByte('\n'); err != nil {
			return fmt.Errorf("log_store: write newline: %w", err)
		}
	}
	return w.Flush()
}

func (l *LogStore) ReadAll() ([]graph.PersistedRewrite, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.Open(l.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("log_store: open for read: %w", err)
	}
	defer f.Close()

	var entries []graph.PersistedRewrite
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, maxLogLineBytes), maxLogLineBytes)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry graph.PersistedRewrite
		if err := json.Unmarshal(line, &entry); err != nil {
			return entries, fmt.Errorf("log_store: unmarshal line: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return entries, fmt.Errorf("log_store: scan: %w", err)
	}
	return entries, nil
}
