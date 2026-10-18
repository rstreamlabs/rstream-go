// See LICENSE file in the project root for license information.

package filesystem

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/net/webdav"
)

const uploadPrefix = ".rstream-upload-"

type uploadKey struct{}

type uploadBody struct {
	io.ReadCloser
	ctx             context.Context
	hash            hash.Hash
	expectedHash    []byte
	expectedSize    int64
	size            int64
	complete        bool
	err             error
	status          int
	receipt         *receiptRecord
	effectCommitted bool
}

func newUploadBody(r *http.Request) (*uploadBody, error) {
	var expected []byte
	if digest := r.Header.Get("Content-Digest"); digest != "" {
		if !strings.HasPrefix(digest, "sha-256=:") || !strings.HasSuffix(digest, ":") {
			return nil, errors.New("Content-Digest must contain one sha-256 byte sequence")
		}
		decoded, err := base64.StdEncoding.DecodeString(digest[len("sha-256=:") : len(digest)-1])
		if err != nil || len(decoded) != sha256.Size {
			return nil, errors.New("invalid sha-256 Content-Digest")
		}
		expected = decoded
	}
	return &uploadBody{ReadCloser: r.Body, ctx: r.Context(), hash: sha256.New(), expectedHash: expected, expectedSize: r.ContentLength}, nil
}

func (b *uploadBody) Read(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		b.err = err
		return 0, err
	}
	n, err := b.ReadCloser.Read(p)
	b.size += int64(n)
	_, _ = b.hash.Write(p[:n])
	if errors.Is(err, io.EOF) {
		b.complete = true
	} else if err != nil {
		b.err = err
	}
	return n, err
}

func (b *uploadBody) validate() error {
	if err := b.ctx.Err(); err != nil {
		b.status = http.StatusRequestTimeout
		return err
	}
	if b.err != nil {
		b.status = http.StatusBadRequest
		var limit *http.MaxBytesError
		if errors.As(b.err, &limit) {
			b.status = http.StatusRequestEntityTooLarge
		}
		return b.err
	}
	if !b.complete || b.expectedSize >= 0 && b.size != b.expectedSize {
		b.status = http.StatusBadRequest
		return io.ErrUnexpectedEOF
	}
	if b.expectedHash != nil && !bytes.Equal(b.expectedHash, b.hash.Sum(nil)) {
		b.status = http.StatusUnprocessableEntity
		return errors.New("upload content digest does not match")
	}
	return nil
}

type atomicWebDAV struct{ *Local }

func (f atomicWebDAV) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	body, ok := ctx.Value(uploadKey{}).(*uploadBody)
	if !ok || flag&os.O_TRUNC == 0 {
		return f.Local.OpenFile(ctx, name, flag, perm)
	}
	if err := f.writeAllowed(ctx); err != nil {
		return nil, err
	}
	target, err := f.resolve(name, false)
	if os.IsNotExist(err) {
		target, err = f.resolve(name, true)
	}
	if err != nil {
		return nil, err
	}
	info, err := f.root.Lstat(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil && !info.Mode().IsRegular() {
		return nil, os.ErrPermission
	}
	if info != nil {
		writable, err := f.root.OpenFile(target, os.O_WRONLY|nonblockFlag, 0)
		if err != nil {
			return nil, err
		}
		if err := writable.Close(); err != nil {
			return nil, err
		}
	}
	temporary := filepath.Join(filepath.Dir(target), uploadPrefix+rand.Text())
	file, err := f.root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_RDWR, perm)
	if err != nil {
		return nil, err
	}
	if info != nil {
		if err := file.Chmod(info.Mode().Perm()); err != nil {
			return nil, errors.Join(err, file.Close(), f.root.Remove(temporary))
		}
	}
	if body.receipt != nil {
		body.receipt.Temporary = temporary
		if err := f.writeReceiptFile(pendingReceipt, body.receipt); err != nil {
			body.status = http.StatusServiceUnavailable
			return nil, errors.Join(err, file.Close(), f.root.Remove(temporary))
		}
	}
	return &atomicUpload{File: file, local: f.Local, body: body, target: target, temporary: temporary}, nil
}

type atomicUpload struct {
	*os.File
	local     *Local
	body      *uploadBody
	target    string
	temporary string
	closed    bool
	writeErr  error
}

func (f *atomicUpload) Write(p []byte) (int, error) {
	n, err := f.File.Write(p)
	if err != nil {
		f.writeErr = err
	} else if n != len(p) {
		f.writeErr = io.ErrShortWrite
	}
	return n, err
}

// Do not promote os.File.ReadFrom: a failed destination write must prevent commit.
func (f *atomicUpload) ReadFrom(r io.Reader) (int64, error) {
	return io.Copy(struct{ io.Writer }{f}, r)
}

func (f *atomicUpload) Close() error {
	if f.closed {
		return os.ErrClosed
	}
	f.closed = true
	if f.writeErr != nil {
		f.body.status = http.StatusInternalServerError
		return errors.Join(f.writeErr, f.File.Close(), f.local.root.Remove(f.temporary))
	}
	if err := f.body.validate(); err != nil {
		return errors.Join(err, f.File.Close(), f.local.root.Remove(f.temporary))
	}
	if err := f.File.Sync(); err != nil {
		f.body.status = http.StatusInternalServerError
		return errors.Join(err, f.File.Close(), f.local.root.Remove(f.temporary))
	}
	if f.body.receipt != nil {
		info, err := f.File.Stat()
		if err == nil {
			f.body.receipt.FileIdentity, err = uploadFileIdentity(info)
		}
		if err != nil {
			f.body.status = http.StatusServiceUnavailable
			return errors.Join(err, f.File.Close(), f.local.root.Remove(f.temporary))
		}
	}
	if err := f.File.Close(); err != nil {
		f.body.status = http.StatusInternalServerError
		return errors.Join(err, f.local.root.Remove(f.temporary))
	}
	if f.body.receipt != nil {
		f.body.receipt.State = "prepared"
		f.body.receipt.Target, f.body.receipt.Temporary = f.target, f.temporary
		if err := f.local.writeReceiptFile(pendingReceipt, f.body.receipt); err != nil {
			f.body.status = http.StatusServiceUnavailable
			return errors.Join(err, f.local.root.Remove(f.temporary))
		}
	}
	if err := f.local.root.Rename(f.temporary, f.target); err != nil {
		f.body.status = http.StatusInternalServerError
		return errors.Join(err, f.local.root.Remove(f.temporary))
	}
	f.body.effectCommitted = true
	if f.body.receipt != nil {
		if err := f.local.syncDirectory(filepath.Dir(f.target)); err != nil {
			f.body.status = http.StatusServiceUnavailable
			return err
		}
		if err := f.local.completeReceipt(f.body.receipt, "committed", http.StatusCreated); err != nil {
			f.body.status = http.StatusServiceUnavailable
			return err
		}
	}
	return nil
}
