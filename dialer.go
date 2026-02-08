// See LICENSE file in the project root for license information.

package rstream

import (
	"context"
	"crypto/tls"
	"net"
)

type Dialer interface {
	Dial(ctx context.Context, addr string, tlsCfg *tls.Config) (net.Conn, error)
}

// TODO : Add UDP Dialer
