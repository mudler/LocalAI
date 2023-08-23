package utils

import (
	"regexp"
	"sync"
)

type RegexpStore struct {
	l     sync.Mutex
	store map[string]*regexp.Regexp
}

func NewRegexpStore() RegexpStore {
	return RegexpStore{
		l:     sync.Mutex{},
		store: make(map[string]*regexp.Regexp),
	}
}

func (rxps *RegexpStore) Get(raw string) (*regexp.Regexp, error) {
	rxps.l.Lock()
	defer rxps.l.Unlock()

	r, exists := rxps.store[raw]
	if exists {
		return r, nil
	}

	c, err := regexp.Compile(raw)
	if err != nil {
		return nil, err
	}
	return c, nil
}
