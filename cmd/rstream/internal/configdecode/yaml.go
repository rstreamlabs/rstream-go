// See LICENSE file in the project root for license information.

// Package configdecode implements strict decoding for CLI-owned YAML files.
package configdecode

import (
	"bytes"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// YAML rejects unknown fields and additional documents, including empty ones.
func YAML(data []byte, value any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("expected exactly one YAML document")
	}
	return nil
}
