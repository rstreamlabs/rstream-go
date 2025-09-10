// See LICENSE file in the project root for license information.

package logging

import "io"

type Config struct {
	Level  string    // debug, info, warn, error
	Format string    // auto, json, json-pretty, text, text-pretty
	Output io.Writer // default: os.Stderr
}
