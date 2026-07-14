package uploaded

import (
	"encoding/json"
	"os"
	"sync"
)

// Files tracks processed file hashes to avoid re-uploading.
type Files struct {
	mu    sync.Mutex
	Files map[string]string `json:"files"`
	Path  string
	store bool
}

// New creates a Files tracker backed by the given JSON file path.
func New(path string) *Files {
	return &Files{
		Files: make(map[string]string),
		Path:  path,
		store: path != "",
	}
}

// NewMemoryOnly creates an in-memory-only Files tracker.
func NewMemoryOnly() *Files {
	return &Files{
		Files: make(map[string]string),
		store: false,
	}
}

// DisablePersistence switches to in-memory only mode.
func (u *Files) DisablePersistence() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.Path = ""
	u.store = false
}

// PersistenceEnabled reports whether file-backed storage is active.
func (u *Files) PersistenceEnabled() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.store
}

// Load reads the upload history from disk. Returns nil on first run (no file).
func (u *Files) Load() error {
	if !u.PersistenceEnabled() {
		return nil
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	f, err := os.Open(u.Path)
	if err != nil {
		if os.IsNotExist(err) {
			u.Files = make(map[string]string)
			return nil
		}
		return err
	}
	defer f.Close()

	return json.NewDecoder(f).Decode(&u.Files)
}

// Save writes the upload history to disk atomically via a temp file rename.
func (u *Files) Save() error {
	if !u.PersistenceEnabled() {
		return nil
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	tmp := u.Path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(u.Files); err != nil {
		f.Close()
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return os.Rename(tmp, u.Path)
}

// IsUploaded checks if a file at the given path with the given hash was already processed.
func (u *Files) IsUploaded(path, hash string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	prev, ok := u.Files[path]
	return ok && prev == hash
}

// MarkUploadedInMemory records a file as processed without saving to disk.
func (u *Files) MarkUploadedInMemory(path, hash string) {
	u.mu.Lock()
	u.Files[path] = hash
	u.mu.Unlock()
}

// MarkUploaded records a file as processed and persists to disk.
func (u *Files) MarkUploaded(path, hash string) error {
	u.MarkUploadedInMemory(path, hash)
	return u.Save()
}
