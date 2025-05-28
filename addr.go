// See LICENSE file in the project root for license information.

package rstream

type Addr struct {
	IdOrName string
}

func (ta *Addr) Network() string { return "rstrm" }

func (ta *Addr) String() string { return ta.IdOrName }
