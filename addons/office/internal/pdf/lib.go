package pdf

import (
	"io"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// newConf returns the default pdfcpu configuration. It reads no file.
func newConf() *model.Configuration {
	return model.NewDefaultConfiguration()
}

// validate runs the pdfcpu validator on rs.
func validate(rs io.ReadSeeker) error {
	return api.Validate(rs, newConf())
}

// pageCount returns the number of pages of rs.
func pageCount(rs io.ReadSeeker) (int, error) {
	return api.PageCount(rs, newConf())
}
