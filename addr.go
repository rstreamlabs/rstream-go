// See LICENSE file in the project root for license information.

package rstream

import "net"

type Addr struct {
	IdOrName string
	SourceIP net.IP
}

func (ta *Addr) Network() string { return "rstrm" }

func (ta *Addr) String() string { return ta.IdOrName }
