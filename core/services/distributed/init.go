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
	Skills   *SkillStore
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

	skills, err := NewSkillStore(db)
	if err != nil {
		return nil, fmt.Errorf("skills store: %w", err)
	}

	xlog.Info("Distributed stores initialized (Gallery, FineTune, Skills)")
	return &Stores{
		Gallery:  gallery,
		FineTune: ft,
		Skills:   skills,
	}, nil
}
