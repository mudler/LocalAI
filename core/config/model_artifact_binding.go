package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/pkg/modelartifacts"
)

func persistArtifactBinding(fileName, modelName string, result modelartifacts.Result) error {
	data, err := os.ReadFile(fileName)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	target, err := findModelMapping(&document, modelName)
	if err != nil {
		return err
	}
	artifactValue := &yaml.Node{}
	encoded, err := yaml.Marshal([]modelartifacts.Spec{result.Spec})
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(encoded, artifactValue); err != nil {
		return err
	}
	setMappingValue(target, "artifacts", artifactValue.Content[0])
	updated, err := yaml.Marshal(&document)
	if err != nil {
		return err
	}
	return writeBindingAtomic(fileName, updated)
}

func findModelMapping(document *yaml.Node, modelName string) (*yaml.Node, error) {
	if len(document.Content) != 1 {
		return nil, fmt.Errorf("invalid model configuration document")
	}
	root := document.Content[0]
	if root.Kind == yaml.MappingNode {
		return root, nil
	}
	if root.Kind == yaml.SequenceNode {
		for _, candidate := range root.Content {
			if candidate.Kind == yaml.MappingNode {
				name := mappingValue(candidate, "name")
				if name != nil && name.Value == modelName {
					return candidate, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("model %q not found in configuration document", modelName)
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content[index+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value,
	)
}

func writeBindingAtomic(fileName string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(fileName), ".artifact-binding-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporaryName, 0644); err != nil {
		return err
	}
	return os.Rename(temporaryName, fileName)
}
