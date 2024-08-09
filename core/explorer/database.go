package explorer

// A simple JSON database for storing and retrieving p2p network tokens and a name and description.

import (
	"encoding/json"
	"os"
	"sort"
	"sync"
)

// Database is a simple JSON database for storing and retrieving p2p network tokens and a name and description.
type Database struct {
	sync.RWMutex
	path string
	data map[string]TokenData
}

// TokenData is a p2p network token with a name and description.
type TokenData struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// NewDatabase creates a new Database with the given path.
func NewDatabase(path string) (*Database, error) {
	db := &Database{
		data: make(map[string]TokenData),
		path: path,
	}
	return db, db.load()
}

// Get retrieves a Token from the Database by its token.
func (db *Database) Get(token string) (TokenData, bool) {
	db.RLock()
	defer db.RUnlock()
	t, ok := db.data[token]
	return t, ok
}

// Set stores a Token in the Database by its token.
func (db *Database) Set(token string, t TokenData) error {
	db.Lock()
	db.data[token] = t
	db.Unlock()

	return db.Save()
}

// Delete removes a Token from the Database by its token.
func (db *Database) Delete(token string) error {
	db.Lock()
	delete(db.data, token)
	db.Unlock()
	return db.Save()
}

func (db *Database) TokenList() []string {
	db.RLock()
	defer db.RUnlock()
	tokens := []string{}
	for k := range db.data {
		tokens = append(tokens, k)
	}

	sort.Slice(tokens, func(i, j int) bool {
		// sort by token
		return tokens[i] < tokens[j]
	})

	return tokens
}

// load reads the Database from disk.
func (db *Database) load() error {
	db.Lock()
	defer db.Unlock()

	if _, err := os.Stat(db.path); os.IsNotExist(err) {
		return nil
	}

	// Read the file from disk
	// Unmarshal the JSON into db.data
	f, err := os.ReadFile(db.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(f, &db.data)
}

// Save writes the Database to disk.
func (db *Database) Save() error {
	db.RLock()
	defer db.RUnlock()

	// Marshal db.data into JSON
	// Write the JSON to the file
	f, err := os.Create(db.path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(db.data)
}
