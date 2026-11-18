package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/mudler/LocalAI/core/services/workerctl"
)

// ErrSplitUnavailable is the error of a frontend that cannot answer the split
// exchange of a node, because it does not share over P2P. The node keeps the
// workers it has.
var ErrSplitUnavailable = errors.New("this frontend does not serve model-split workers")

// NodeSplitReport is the last report of a backend node about the model-split
// workers attached to it: the P2P endpoint of the node and the live state of
// each worker. One row per node, replaced by every report, so that every
// frontend shows the same state.
type NodeSplitReport struct {
	NodeID string `gorm:"primaryKey;size:36"`
	// Report is the JSON of workerctl.SplitReport.
	Report    string `gorm:"type:text"`
	UpdatedAt time.Time
}

// TableName is the table of the reports.
func (NodeSplitReport) TableName() string { return "node_split_reports" }

// StoredSplitReport is a report as the frontends read it.
type StoredSplitReport struct {
	NodeID    string
	Report    workerctl.SplitReport
	UpdatedAt time.Time
}

// SetSplitReport replaces the split report of a node.
func (r *NodeRegistry) SetSplitReport(ctx context.Context, nodeID string, rep workerctl.SplitReport) error {
	b, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	row := NodeSplitReport{NodeID: nodeID, Report: string(b), UpdatedAt: time.Now().UTC()}
	err = r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "node_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"report", "updated_at"}),
	}).Create(&row).Error
	if err != nil {
		return fmt.Errorf("storing the split report of node %s: %w", nodeID, err)
	}
	return nil
}

// SplitReport returns the split report of a node, and false when it sent
// none.
func (r *NodeRegistry) SplitReport(ctx context.Context, nodeID string) (StoredSplitReport, bool, error) {
	var row NodeSplitReport
	err := r.db.WithContext(ctx).Where("node_id = ?", nodeID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return StoredSplitReport{}, false, nil
	}
	if err != nil {
		return StoredSplitReport{}, false, fmt.Errorf("reading the split report of node %s: %w", nodeID, err)
	}
	rep, err := row.decode()
	return rep, err == nil, err
}

// SplitReports returns the split reports of every node, by node ID.
func (r *NodeRegistry) SplitReports(ctx context.Context) (map[string]StoredSplitReport, error) {
	var rows []NodeSplitReport
	if err := r.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("listing the split reports: %w", err)
	}
	out := make(map[string]StoredSplitReport, len(rows))
	for _, row := range rows {
		rep, err := row.decode()
		if err != nil {
			continue
		}
		out[row.NodeID] = rep
	}
	return out, nil
}

func (row NodeSplitReport) decode() (StoredSplitReport, error) {
	var rep workerctl.SplitReport
	if err := json.Unmarshal([]byte(row.Report), &rep); err != nil {
		return StoredSplitReport{}, fmt.Errorf("reading the split report of node %s: %w", row.NodeID, err)
	}
	return StoredSplitReport{NodeID: row.NodeID, Report: rep, UpdatedAt: row.UpdatedAt}, nil
}
