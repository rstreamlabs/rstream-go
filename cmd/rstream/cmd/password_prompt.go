// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/term"
)

func readPasswordContext(ctx context.Context, input *os.File) ([]byte, error) {
	state, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = input.SetReadDeadline(time.Time{})
		_ = term.Restore(int(input.Fd()), state)
	}()
	value := make([]byte, 0, 64)
	buffer := make([]byte, 1)
	deadlines := true
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if deadlines {
			if err := input.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
				if !errors.Is(err, os.ErrNoDeadline) {
					return nil, err
				}
				deadlines = false
			}
		}
		n, err := input.Read(buffer)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			if errors.Is(err, io.EOF) && len(value) > 0 {
				return value, nil
			}
			return nil, err
		}
		if n == 0 {
			continue
		}
		switch buffer[0] {
		case '\r', '\n':
			return value, nil
		case 3:
			return nil, context.Canceled
		case 4:
			if len(value) == 0 {
				return nil, io.EOF
			}
			return value, nil
		case 8, 127:
			if len(value) > 0 {
				value = value[:len(value)-1]
			}
		case 21:
			value = value[:0]
		default:
			value = append(value, buffer[0])
			if len(value) > 4096 {
				return nil, errors.New("password must contain at most 4096 bytes")
			}
		}
	}
}
