// See LICENSE file in the project root for license information.

package logging

import "io"

type Config struct {
	Level  string    // debug, info, warn, error, none
	Format string    // json, text, pretty
	Output io.Writer // default: os.Stdout
}
