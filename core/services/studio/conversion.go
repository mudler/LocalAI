package studio

import (
	"context"

	"github.com/mudler/LocalAI/core/services/studio/convert"
)

// ConversionSource keeps the report of the conversion of the old chat, agents
// and skills. convert.Marks implements it.
type ConversionSource interface {
	Report(ctx context.Context) (convert.Report, bool, error)
}

// SetConversion gives the service the source of the conversion report. The
// application calls it once, before the service is shared.
func (s *Service) SetConversion(c ConversionSource) { s.conversion = c }

// Conversion returns the report of the last conversion, with the items of every
// user, and whether there is one. The caller picks the items a user may see.
func (s *Service) Conversion(ctx context.Context) (convert.Report, bool, error) {
	if s.conversion == nil {
		return convert.Report{}, false, nil
	}
	return s.conversion.Report(ctx)
}
