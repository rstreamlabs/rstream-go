// See LICENSE file in the project root for license information.

package rstream

type rawJSON []byte

func (r rawJSON) MarshalJSON() ([]byte, error) { return r, nil }
func (r rawJSON) String() string               { return string(r) }
