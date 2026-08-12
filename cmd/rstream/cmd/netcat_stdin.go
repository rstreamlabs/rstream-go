// See LICENSE file in the project root for license information.

package cmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
)

type netcatContextReader interface {
	ReadContext(context.Context, []byte) (int, error)
}

func resolveNetcatStdinRead(cfg *netcatClientConfig) (func(context.Context, []byte) (int, error), error) {
	return resolveNetcatReaderRead(cfg.Stdin, cfg.StdinReadContext)
}

func resolveNetcatReaderRead(reader io.Reader, readContext func(context.Context, []byte) (int, error)) (func(context.Context, []byte) (int, error), error) {
	if readContext != nil {
		return readContext, nil
	}
	if contextReader, ok := reader.(netcatContextReader); ok {
		return contextReader.ReadContext, nil
	}
	if readStdin := netcatFileStdinRead(reader); readStdin != nil {
		return readStdin, nil
	}
	switch reader.(type) {
	case *bytes.Buffer, *bytes.Reader, *strings.Reader:
		return func(ctx context.Context, buffer []byte) (int, error) {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			return reader.Read(buffer)
		}, nil
	default:
		return nil, fmt.Errorf("stdin reader must support cancellation through StdinReadContext, ReadContext, or an OS file descriptor")
	}
}
