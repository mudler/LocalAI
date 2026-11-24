// SPDX-License-Identifier: MIT
package localai

import (
	"errors"
	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/core/systemone"
	"net/http"
)

const systemOneMaxImageBytes = systemone.MaxImageEncodedBytes

var errSystemOneImagesUnsupported = errors.New("image input is not supported by the NER decision path")

func systemOneInputStatus(err error) int {
	if errors.Is(err, errSystemOneImagesUnsupported) {
		return http.StatusNotImplemented
	}
	var validation *systemone.ValidationError
	if errors.As(err, &validation) {
		switch validation.Kind {
		case systemone.InputTooLarge:
			return http.StatusRequestEntityTooLarge
		case systemone.UnsupportedBackend:
			return http.StatusNotImplemented
		}
	}
	return http.StatusBadRequest
}
func systemOneImages(req *schema.SystemOneRequest) ([]string, error) {
	return systemone.CollectImages(req)
}
func validateSystemOneImages(req *schema.SystemOneRequest) error {
	images, err := systemone.CollectImages(req)
	if err != nil {
		return err
	}
	return systemone.ValidateImages(images)
}
