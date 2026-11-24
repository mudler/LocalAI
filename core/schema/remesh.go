// SPDX-License-Identifier: MIT
package schema

import "fmt"

func MeshRemeshParameters() []ThreeDParameter {
	return []ThreeDParameter{
		{Name: "mode", Label: "Remeshing mode", Type: "enum", Default: "wrap_quad", Options: []string{"wrap_quad", "wrap", "quad"}},
		{Name: "alpha_ratio", Label: "Wrap detail (fraction of diagonal)", Type: "number", Default: "0.005", Min: 0.0035, Max: 0.025},
		{Name: "target_quads", Label: "Target quads (density hint)", Type: "integer", Default: "20000", Min: 100, Max: 100000},
		{Name: "adaptivity", Label: "Curvature adaptivity", Type: "number", Default: "1", Min: 0, Max: 1},
		{Name: "texture_size", Label: "Texture size", Type: "integer", Default: "2048", Min: 256, Max: 4096},
	}
}

func ValidateMeshRemeshParameters(params map[string]string) error {
	known := map[string]ThreeDParameter{}
	for _, p := range MeshRemeshParameters() {
		known[p.Name] = p
	}
	for name, value := range params {
		p, ok := known[name]
		if !ok {
			return fmt.Errorf("unsupported remeshing parameter %q", name)
		}
		if err := p.Validate(value); err != nil {
			return err
		}
	}
	return nil
}
