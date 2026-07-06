package uploaded

import (
	"encoding/json"
	"os"
	"sync"
)

type Files struct {
	mu    sync.Mutex
	Files map[string]string `json:"files"`
	Path  string
	store bool
}

func New(path string) *Files {
	return &Files{
		Files: make(map[string]string),
		Path:  path,
		store: path != "",
	}
}

func NewMemoryOnly() *Files {
	return &Files{
		Files: make(map[string]string),
		Path:  "",
		store: false,
	}
}

func (u *Files) DisablePersistence() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.Path = ""
	u.store = false
}

func (u *Files) PersistenceEnabled() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.store
}

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
	dec := json.NewDecoder(f)
	return dec.Decode(&u.Files)
}

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
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(u.Files); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return os.Rename(tmp, u.Path)
}

func (u *Files) IsUploaded(path, hash string) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	prev, ok := u.Files[path]
	return ok && prev == hash
}

func (u *Files) MarkUploadedInMemory(path, hash string) {
	u.mu.Lock()
	u.Files[path] = hash
	u.mu.Unlock()
}

func (u *Files) MarkUploaded(path, hash string) error {
	u.MarkUploadedInMemory(path, hash)
	return u.Save()
}
