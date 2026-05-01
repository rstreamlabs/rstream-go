// See LICENSE file in the project root for license information.

package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func writeStructuredOutput(format string, value any) error {
	switch format {
	case "json":
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(append(data, '\n'))
		return err
	case "yaml":
		data, err := yaml.Marshal(value)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(data)
		return err
	default:
		return fmt.Errorf("unsupported output format: %s", format)
	}
}

func writeOptionalStructuredOutput(format string, value any) error {
	switch format {
	case "", "none":
		return nil
	case "json", "yaml":
		return writeStructuredOutput(format, value)
	default:
		return validateOutputMode(format, "none", "json", "yaml")
	}
}
