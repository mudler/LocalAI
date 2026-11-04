package proposals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mudler/LocalAI/core/services/studio/internal/dberr"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// proposalRow is the table of change proposals. The source is JSON text so
// one schema works on SQLite and PostgreSQL.
type proposalRow struct {
	ID        string `gorm:"primaryKey;size:64"`
	Owner     string `gorm:"index:idx_studio_change_owner,priority:1;size:255"`
	Agent     string `gorm:"size:64"`
	Kind      string `gorm:"size:16"`
	Scope     string `gorm:"size:16"`
	Target    string `gorm:"size:512"`
	Summary   string `gorm:"size:512"`
	Content   string
	BaseHash  string `gorm:"size:64"`
	Source    string
	Status    string `gorm:"index:idx_studio_change_owner,priority:2;size:16"`
	Rev       int64
	RevertRev int64
	Created   time.Time
	Expires   time.Time
	Answered  *time.Time
	Auto      bool `gorm:"not null;default:false"` // a note applied at once; the default keeps older rows readable
}

func (proposalRow) TableName() string { return "studio_change_proposals" }

// eventRow is the audit table. Seq orders it.
type eventRow struct {
	Seq      int64  `gorm:"primaryKey;autoIncrement"`
	Owner    string `gorm:"index;size:255"`
	At       time.Time
	Action   string `gorm:"size:16"`
	Proposal string `gorm:"size:64"`
	Body     string
}

func (eventRow) TableName() string { return "studio_change_events" }

func (r proposalRow) proposal() (contract.ChangeProposal, error) {
	p := contract.ChangeProposal{V: contract.ChangeProposalVersion, ID: r.ID, Owner: r.Owner, Agent: r.Agent,
		Kind: contract.ChangeKind(r.Kind), Scope: contract.ChangeScope(r.Scope), Target: r.Target, Summary: r.Summary,
		Content: r.Content, BaseHash: r.BaseHash, Status: contract.ProposalStatus(r.Status), Auto: r.Auto, Rev: r.Rev, RevertRev: r.RevertRev,
		Created: r.Created.UTC(), Expires: r.Expires.UTC()}
	if r.Answered != nil {
		p.Answered = r.Answered.UTC()
	}
	if r.Source != "" {
		if err := json.Unmarshal([]byte(r.Source), &p.Source); err != nil {
			return p, fmt.Errorf("proposals: read the source of %s: %w", r.ID, err)
		}
	}
	return p, nil
}

func rowOf(p contract.ChangeProposal) (proposalRow, error) {
	src, err := json.Marshal(p.Source)
	if err != nil {
		return proposalRow{}, err
	}
	r := proposalRow{ID: p.ID, Owner: p.Owner, Agent: p.Agent, Kind: string(p.Kind), Scope: string(p.Scope), Target: p.Target,
		Summary: p.Summary, Content: p.Content, BaseHash: p.BaseHash, Source: string(src), Status: string(p.Status), Auto: p.Auto,
		Rev: p.Rev, RevertRev: p.RevertRev, Created: p.Created, Expires: p.Expires}
	if !p.Answered.IsZero() {
		a := p.Answered
		r.Answered = &a
	}
	return r, nil
}

type gormStore struct{ db *gorm.DB }

// NewGORM returns a Store on db (SQLite or PostgreSQL) and creates its tables.
func NewGORM(db *gorm.DB) (Store, error) {
	if db == nil {
		return nil, errors.New("proposals: nil *gorm.DB")
	}
	if err := db.AutoMigrate(&proposalRow{}, &eventRow{}); err != nil {
		return nil, fmt.Errorf("proposals: migrate: %w", err)
	}
	return &gormStore{db: db}, nil
}

func (s *gormStore) Create(ctx context.Context, p contract.ChangeProposal, lim Limits) error {
	row, err := rowOf(p)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if lim.MaxPending > 0 {
			var n int64
			if err := tx.Model(&proposalRow{}).Where("owner = ? AND status = ?", p.Owner, string(contract.ProposalPending)).Count(&n).Error; err != nil {
				return err
			}
			if int(n) >= lim.MaxPending {
				return ErrTooManyPending
			}
		}
		if lim.PerWindow > 0 && lim.Window > 0 {
			var n int64
			if err := tx.Model(&proposalRow{}).Where("owner = ? AND agent = ? AND created > ?", p.Owner, p.Agent, p.Created.Add(-lim.Window)).Count(&n).Error; err != nil {
				return err
			}
			if int(n) >= lim.PerWindow {
				return ErrRateLimited
			}
		}
		if err := tx.Create(&row).Error; err != nil {
			if dberr.IsUniqueViolation(err) {
				return ErrExists
			}
			return err
		}
		return nil
	})
}

func (s *gormStore) Get(ctx context.Context, owner, id string) (contract.ChangeProposal, error) {
	var r proposalRow
	err := s.db.WithContext(ctx).Where("id = ? AND owner = ?", id, owner).Take(&r).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return contract.ChangeProposal{}, ErrNotFound
	}
	if err != nil {
		return contract.ChangeProposal{}, err
	}
	return r.proposal()
}

func (s *gormStore) List(ctx context.Context, owner string, statuses ...contract.ProposalStatus) ([]contract.ChangeProposal, error) {
	q := s.db.WithContext(ctx).Where("owner = ?", owner)
	if len(statuses) > 0 {
		st := make([]string, len(statuses))
		for i, v := range statuses {
			st[i] = string(v)
		}
		q = q.Where("status IN ?", st)
	}
	var rows []proposalRow
	if err := q.Order("created DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]contract.ChangeProposal, 0, len(rows))
	for _, r := range rows {
		p, err := r.proposal()
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *gormStore) Settle(ctx context.Context, owner, id string, from, to contract.ProposalStatus, at time.Time, rev int64) (bool, error) {
	set := map[string]any{"status": string(to), "answered": at}
	if at.IsZero() {
		set["answered"] = nil
	}
	if to == contract.ProposalReverted {
		set["revert_rev"] = rev
	}
	res := s.db.WithContext(ctx).Model(&proposalRow{}).Where("id = ? AND owner = ? AND status = ?", id, owner, string(from)).Updates(set)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		if _, err := s.Get(ctx, owner, id); err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

func (s *gormStore) SetRev(ctx context.Context, owner, id string, rev int64) error {
	res := s.db.WithContext(ctx).Model(&proposalRow{}).Where("id = ? AND owner = ?", id, owner).Update("rev", rev)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *gormStore) Expire(ctx context.Context, now time.Time) ([]contract.ChangeProposal, error) {
	var out []contract.ChangeProposal
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []proposalRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("status = ? AND expires <= ?", string(contract.ProposalPending), now).Find(&rows).Error; err != nil {
			return err
		}
		for _, r := range rows {
			// Only the frontend whose update changes the row reports it.
			res := tx.Model(&proposalRow{}).Where("id = ? AND status = ?", r.ID, string(contract.ProposalPending)).
				Updates(map[string]any{"status": string(contract.ProposalExpired), "answered": now})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				continue
			}
			p, err := r.proposal()
			if err != nil {
				return err
			}
			p.Status, p.Answered = contract.ProposalExpired, now
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *gormStore) AddEvent(ctx context.Context, ev Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Create(&eventRow{Owner: ev.Owner, At: ev.At, Action: ev.Action, Proposal: ev.Proposal, Body: string(body)}).Error
}

func (s *gormStore) Events(ctx context.Context, owner string, limit int) ([]Event, error) {
	var rows []eventRow
	if err := s.db.WithContext(ctx).Where("owner = ?", owner).Order("seq DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Event, 0, len(rows))
	for _, r := range rows {
		var ev Event
		if err := json.Unmarshal([]byte(r.Body), &ev); err != nil {
			return nil, fmt.Errorf("proposals: read the audit: %w", err)
		}
		out = append(out, ev)
	}
	return out, nil
}
