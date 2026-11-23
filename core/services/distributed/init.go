package distributed

import (
	"fmt"

	"github.com/mudler/xlog"
	"gorm.io/gorm"
)

// Stores holds all Phase 4 distributed stores.
type Stores struct {
	Gallery  *GalleryStore
	FineTune *FineTuneStore
	Quant    *QuantStore
	// UpgradeRuns keeps the server-owned upgrade runs. Standalone servers
	// open it on their own database with NewUpgradeRunStore.
	UpgradeRuns *UpgradeRunStore
}

// InitStores creates and migrates all Phase 4 distributed stores.
func InitStores(db *gorm.DB) (*Stores, error) {
	gallery, err := NewGalleryStore(db)
	if err != nil {
		return nil, fmt.Errorf("gallery store: %w", err)
	}

	ft, err := NewFineTuneStore(db)
	if err != nil {
		return nil, fmt.Errorf("fine-tune store: %w", err)
	}

	quant, err := NewQuantStore(db)
	if err != nil {
		return nil, fmt.Errorf("quantization store: %w", err)
	}

	upgradeRuns, err := NewUpgradeRunStore(db)
	if err != nil {
		return nil, fmt.Errorf("upgrade run store: %w", err)
	}

	xlog.Info("Distributed stores initialized (Gallery, FineTune, Quant, UpgradeRuns)")
	return &Stores{
		Gallery:     gallery,
		FineTune:    ft,
		Quant:       quant,
		UpgradeRuns: upgradeRuns,
	}, nil
}
