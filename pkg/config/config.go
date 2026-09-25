package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Load reads one YAML document into a service-owned configuration struct.
// Only ${NAME} placeholders are expanded, after parsing, so values cannot inject YAML.
func Load(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read configuration: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var node yaml.Node
	if err := decoder.Decode(&node); err != nil {
		return errors.New("invalid YAML configuration")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("configuration must contain exactly one document")
	}
	if err := expand(&node); err != nil {
		return err
	}
	data, err = yaml.Marshal(&node)
	if err != nil {
		return errors.New("cannot normalize configuration")
	}
	decoder = yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return errors.New("configuration contains unknown fields or invalid values")
	}
	return nil
}
func expand(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		var missing string
		node.Value = envPattern.ReplaceAllStringFunc(node.Value, func(token string) string {
			key := token[2 : len(token)-1]
			value, ok := os.LookupEnv(key)
			if !ok {
				missing = key
			}
			return value
		})
		if missing != "" {
			return fmt.Errorf("required environment variable %s is not set", missing)
		}
	}
	for _, child := range node.Content {
		if err := expand(child); err != nil {
			return err
		}
	}
	return nil
}
