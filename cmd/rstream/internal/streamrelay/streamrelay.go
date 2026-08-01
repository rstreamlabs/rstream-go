// See LICENSE file in the project root for license information.

package streamrelay

import (
	"io"
	"net"
)

func Bidirectional(left, right net.Conn) {
	errs := make(chan error, 2)
	go func() {
		errs <- copyAndCloseWrite(right, left)
	}()
	go func() {
		errs <- copyAndCloseWrite(left, right)
	}()
	if err := <-errs; err != nil {
		_ = left.Close()
		_ = right.Close()
	}
	<-errs
}

func copyAndCloseWrite(dst, src net.Conn) error {
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	if conn, ok := dst.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return dst.Close()
}
