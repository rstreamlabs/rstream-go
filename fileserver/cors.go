// See LICENSE file in the project root for license information.

package fileserver

import "github.com/rstreamlabs/rstream-go/httpcors"

// NormalizeCORS fills WebDAV defaults for an explicitly enabled policy.
// The returned policy can also be used as TunnelProperties.HTTPCORS.
func NormalizeCORS(config *httpcors.Config) (*httpcors.Config, error) {
	if config == nil {
		return nil, nil
	}
	config = config.Clone()
	if len(config.AllowedMethods) == 0 {
		config.AllowedMethods = []string{"GET", "HEAD", "OPTIONS", "PROPFIND", "PUT", "DELETE", "MKCOL", "COPY", "MOVE", "PROPPATCH", "LOCK", "UNLOCK"}
	}
	if len(config.AllowedHeaders) == 0 {
		config.AllowedHeaders = []string{"Authorization", "Content-Type", "Content-Digest", "Range", "If-Range", "Depth", "Destination", "Overwrite", "If", "If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "Lock-Token", "Timeout"}
	}
	if len(config.ExposedHeaders) == 0 {
		config.ExposedHeaders = []string{"ETag", "Content-Disposition", "Content-Range", "Accept-Ranges", "Last-Modified", "Content-Length", "Allow", "DAV", "Lock-Token", "Location", "Retry-After", "WWW-Authenticate"}
	}
	return httpcors.Normalize(config)
}
