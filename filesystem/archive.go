// See LICENSE file in the project root for license information.

package filesystem

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path"
)

// WriteZIP streams a directory without staging file contents in memory or on
// disk. Directory depth and entry count are bounded; symlink cycles fail.
func (f *Local) WriteZIP(ctx context.Context, output io.Writer, name string) error {
	info, err := f.Stat(ctx, name)
	if err != nil {
		return err
	}
	if !info.IsDir() || f.IsFile() {
		return fmt.Errorf("archive target must be a directory")
	}
	writer := zip.NewWriter(output)
	state := archiveState{writer: writer, local: f, buffer: make([]byte, 64<<10)}
	if err := state.walk(ctx, name, "", nil); err != nil {
		return err
	}
	return writer.Close()
}

type archiveState struct {
	writer *zip.Writer
	local  *Local
	buffer []byte
	count  int
}

func (a *archiveState) walk(ctx context.Context, source, destination string, parents []os.FileInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(parents) >= 64 || a.count >= 100000 {
		return fmt.Errorf("archive exceeds the depth or entry limit")
	}
	file, err := a.local.OpenFile(ctx, source, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	a.count++
	if info.IsDir() {
		for _, parent := range parents {
			if os.SameFile(info, parent) {
				return fmt.Errorf("archive contains a directory cycle")
			}
		}
		if destination != "" {
			if _, err := a.writer.CreateHeader(&zip.FileHeader{Name: destination + "/", Method: zip.Store, Modified: info.ModTime()}); err != nil {
				return err
			}
		}
		items, err := file.Readdir(0)
		if err != nil {
			return err
		}
		for _, item := range items {
			if err := a.walk(ctx, path.Join(source, item.Name()), path.Join(destination, item.Name()), append(parents, info)); err != nil {
				return err
			}
		}
		return nil
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = destination
	header.Method = zip.Store
	output, err := a.writer.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.CopyBuffer(contextWriter{ctx: ctx, output: output}, file, a.buffer)
	return err
}

type contextWriter struct {
	ctx    context.Context
	output io.Writer
}

func (w contextWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.output.Write(data)
}
