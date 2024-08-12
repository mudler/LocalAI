package explorer

// A simple JSON database for storing and retrieving p2p network tokens and a name and description.

import (
	"encoding/json"
	"os"
	"sort"
	"sync"

	"github.com/gofrs/flock"
)

// Database is a simple JSON database for storing and retrieving p2p network tokens and a name and description.
type Database struct {
	path  string
	data  map[string]TokenData
	flock *flock.Flock
	sync.Mutex
}

// TokenData is a p2p network token with a name and description.
type TokenData struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Clusters    []ClusterData
	Failures    int
}

type ClusterData struct {
	Workers   []string
	Type      string
	NetworkID string
}

// NewDatabase creates a new Database with the given path.
func NewDatabase(path string) (*Database, error) {
	fileLock := flock.New(path + ".lock")
	db := &Database{
		data:  make(map[string]TokenData),
		path:  path,
		flock: fileLock,
	}
	return db, db.load()
}

// Get retrieves a Token from the Database by its token.
func (db *Database) Get(token string) (TokenData, bool) {
	db.flock.Lock() // we are making sure that the file is not being written to
	defer db.flock.Unlock()
	db.Lock() // we are making sure that is safe if called by another instance in the same process
	defer db.Unlock()
	db.load()
	t, ok := db.data[token]
	return t, ok
}

// Set stores a Token in the Database by its token.
func (db *Database) Set(token string, t TokenData) error {
	db.flock.Lock()
	defer db.flock.Unlock()
	db.Lock()
	defer db.Unlock()
	db.load()
	db.data[token] = t

	return db.save()
}

// Delete removes a Token from the Database by its token.
func (db *Database) Delete(token string) error {
	db.flock.Lock()
	defer db.flock.Unlock()
	db.Lock()
	defer db.Unlock()
	db.load()
	delete(db.data, token)
	return db.save()
}

func (db *Database) TokenList() []string {
	db.flock.Lock()
	defer db.flock.Unlock()
	db.Lock()
	defer db.Unlock()
	db.load()
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
func (db *Database) save() error {
	// Marshal db.data into JSON
	// Write the JSON to the file
	f, err := os.Create(db.path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(db.data)
}
