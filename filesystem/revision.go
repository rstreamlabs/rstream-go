// See LICENSE file in the project root for license information.

package filesystem

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// A content revision is independent of the filesystem timestamp granularity.
// The descriptor is also the one used by GET, so an atomic replacement cannot
// pair a revision from the new inode with bytes from the previous inode.
type revisionInfo struct {
	os.FileInfo
	revision func(context.Context) (string, error)
}

func (i revisionInfo) ETag(ctx context.Context) (string, error) { return i.revision(ctx) }

// os.SameFile only recognizes the concrete metadata returned by os.Stat.
func systemFileInfo(info os.FileInfo) os.FileInfo {
	switch value := info.(type) {
	case revisionInfo:
		return systemFileInfo(value.FileInfo)
	case namedInfo:
		return systemFileInfo(value.FileInfo)
	default:
		return info
	}
}

func contentRevision(ctx context.Context, file *os.File, info os.FileInfo) (string, error) {
	if info.IsDir() {
		return fileRevision(info), nil
	}
	h := sha256.New()
	buffer := make([]byte, 64<<10)
	var offset int64
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := file.ReadAt(buffer, offset)
		_, _ = h.Write(buffer[:n])
		offset += int64(n)
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	after, err := file.Stat()
	if err != nil {
		return "", err
	}
	if offset != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return "", fmt.Errorf("file changed while reading its revision")
	}
	return digestRevision(h.Sum(nil)), nil
}

func digestRevision(digest []byte) string { return fmt.Sprintf(`"sha256-%x"`, digest) }

func (f *localFile) Stat() (os.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	return revisionInfo{info, func(ctx context.Context) (string, error) {
		return contentRevision(ctx, f.File, info)
	}}, nil
}

// WebDAV requests this metadata before Close and its ETag after Close. Capture
// the already streamed digest; do not reopen the replacement by its pathname.
func (f *atomicUpload) Stat() (os.FileInfo, error) {
	info, err := f.File.Stat()
	if err != nil {
		return nil, err
	}
	revision := digestRevision(f.body.hash.Sum(nil))
	return revisionInfo{info, func(context.Context) (string, error) { return revision, nil }}, nil
}
