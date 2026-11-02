// SPDX-License-Identifier: MIT
package store

import (
	"context"
	"encoding/json"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func memoryRequestKey(agent, invocation string) string {
	b, _ := json.Marshal([]string{agent, invocation})
	return string(b)
}

func (s *jsonStore) FrozenMemory(_ context.Context, principal, chat, agent, invocation string, create func() ([]byte, error)) ([]byte, error) {
	s.memoryMu.Lock()
	defer s.memoryMu.Unlock()
	var out []byte
	err := s.edit(principal, chat, func(next *chatState) error {
		out = append([]byte(nil), next.FrozenMemoryRequests[memoryRequestKey(agent, invocation)]...)
		return nil
	})
	if err != nil || len(out) != 0 || create == nil {
		return out, err
	}
	candidate, err := create()
	if err != nil {
		return nil, err
	}
	err = s.edit(principal, chat, func(next *chatState) error {
		requests := make(map[string][]byte, len(next.FrozenMemoryRequests)+1)
		for k, v := range next.FrozenMemoryRequests {
			requests[k] = v
		}
		out = append([]byte(nil), candidate...)
		requests[memoryRequestKey(agent, invocation)] = out
		next.FrozenMemoryRequests = requests
		return nil
	})
	return append([]byte(nil), out...), err
}

type memoryRequestRow struct {
	ChatID     string `gorm:"primaryKey;size:128"`
	AgentID    string `gorm:"primaryKey;size:128"`
	Invocation string `gorm:"primaryKey;size:128"`
	Payload    []byte
}

func (memoryRequestRow) TableName() string { return "studio_memory_requests" }

func (s *gormStore) FrozenMemory(ctx context.Context, principal, chat, agent, invocation string, create func() ([]byte, error)) ([]byte, error) {
	s.memoryMu.Lock()
	defer s.memoryMu.Unlock()
	read := func(tx *gorm.DB) ([]byte, error) {
		if _, err := lock(tx, principal, chat, "goal_rev"); err != nil {
			return nil, err
		}
		var found memoryRequestRow
		err := tx.Where("chat_id = ? AND agent_id = ? AND invocation = ?", chat, agent, invocation).Limit(1).Find(&found).Error
		return append([]byte(nil), found.Payload...), err
	}
	var out []byte
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = read(tx)
		return err
	})
	if err != nil || len(out) != 0 || create == nil {
		return out, err
	}
	// Preparation performs authorization reads and may call the model. It must
	// not hold a transaction (or the only database connection) while doing so.
	// Separate store handles may prepare competing candidates; the transaction
	// below keeps the first durable candidate, not globally exactly-once preparation.
	candidate, err := create()
	if err != nil {
		return nil, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = read(tx)
		if err != nil || len(out) != 0 {
			return err
		}
		row := memoryRequestRow{ChatID: chat, AgentID: agent, Invocation: invocation, Payload: candidate}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		out, err = read(tx)
		return err
	})
	return out, err
}
