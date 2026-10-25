// See LICENSE file in the project root for license information.

package filesystem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const receiptDirectory = uploadPrefix + "receipts"
const pendingReceipt = uploadPrefix + "pending"
const receiptLimit = 4096

// DurableUploadsSupported reports whether this build can durably synchronize
// both the uploaded file and its namespace journal. Clients must also check
// the service's current write permission and request admission.
func DurableUploadsSupported() bool { return durableUploads }

// UploadReceipt describes one attempt. An existing ID never starts another
// write, including after interruption or an uncertain result. It is scoped to
// this export and the currently authorized path, not a bearer capability.
type UploadReceipt struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	State       string `json:"state"`
	Status      int    `json:"status"`
	Size        string `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	Revision    string `json:"revision,omitempty"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type receiptRecord struct {
	UploadReceipt
	Fingerprint  string `json:"fingerprint"`
	Target       string `json:"target,omitempty"`
	Temporary    string `json:"temporary,omitempty"`
	FileIdentity string `json:"file_identity,omitempty"`
}

func receiptName(id string) string {
	return filepath.Join(receiptDirectory, fmt.Sprintf("%x.json", sha256.Sum256([]byte(id))))
}

func validUploadID(id string) bool {
	if len(id) < 16 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func (f *Local) newReceipt(r *http.Request, name string, body *uploadBody) (*receiptRecord, error) {
	id := r.Header.Get("Upload-Id")
	if !validUploadID(id) || body.expectedHash == nil {
		return nil, fmt.Errorf("Upload-Id and SHA-256 Content-Digest are required")
	}
	size := r.Header.Get("Upload-Length")
	n, err := strconv.ParseInt(size, 10, 64)
	if err != nil || n < 0 || strconv.FormatInt(n, 10) != size || r.ContentLength >= 0 && n != r.ContentLength {
		return nil, fmt.Errorf("Upload-Length must match the complete content length")
	}
	body.expectedSize = n
	relative, err := f.lexical(name)
	if err != nil {
		return nil, err
	}
	// Bind the exact conditional request. Changing its destination, length,
	// digest or preconditions under an existing ID is a conflict.
	identity, _ := json.Marshal([]string{r.Method, filepath.ToSlash(relative), size, hex.EncodeToString(body.expectedHash), r.Header.Get("If-Match"), r.Header.Get("If-None-Match")})
	return &receiptRecord{UploadReceipt: UploadReceipt{ID: id, Path: filepath.ToSlash(relative), State: "receiving", Size: size, SHA256: hex.EncodeToString(body.expectedHash), StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}, Fingerprint: fmt.Sprintf("%x", sha256.Sum256(identity))}, nil
}

func (f *Local) readReceiptFile(name string) (*receiptRecord, error) {
	data, err := readJournalFile(f.journal, name)
	if err != nil {
		return nil, err
	}
	var record receiptRecord
	err = json.Unmarshal(data, &record)
	if err != nil {
		return nil, err
	}
	if !validUploadID(record.ID) || len(record.Fingerprint) != 64 {
		return nil, fmt.Errorf("invalid upload journal")
	}
	return &record, nil
}

func (f *Local) writeReceiptFile(name string, record *receiptRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return writeJournalFile(f.journal, name, data)
}

func (f *Local) beginReceipt(record *receiptRecord) error {
	target, err := f.resolve(record.Path, false)
	if os.IsNotExist(err) {
		target, err = f.resolve(record.Path, true)
	}
	if err != nil {
		return err
	}
	record.Target = target
	receipts, err := f.openReceiptDirectory()
	if err != nil {
		return err
	}
	defer receipts.Close()
	directory, err := receipts.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := directory.Readdirnames(receiptLimit)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(entries) >= receiptLimit {
		return errReceiptCapacity
	}
	// Check the receipt destination before modifying content, too. A writable
	// pending journal alone cannot guarantee publication of the terminal receipt.
	if err := probeJournal(receipts); err != nil {
		return err
	}
	if err := syncRootDirectory(f.journal, "."); err != nil {
		return err
	}
	return f.writeReceiptFile(pendingReceipt, record)
}

func (f *Local) openReceiptDirectory() (*os.Root, error) {
	if err := f.journal.Mkdir(receiptDirectory, 0o700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := f.journal.Lstat(receiptDirectory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, os.ErrPermission
	}
	root, err := f.journal.OpenRoot(receiptDirectory)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, os.ErrPermission
	}
	return root, nil
}

func (f *Local) completeReceipt(record *receiptRecord, state string, status int) error {
	record.State, record.Status = state, status
	record.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if state == "committed" {
		record.Revision = `"sha256-` + record.SHA256 + `"`
	}
	if err := f.writeReceiptFile(receiptName(record.ID), record); err != nil {
		return err
	}
	if err := f.journal.Remove(pendingReceipt); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncRootDirectory(f.journal, ".")
}

// Must hold the namespace mutation lock. Recovery runs before a later writer,
// so replacing the current file cannot erase evidence of an earlier commit.
// It only records the outcome; it never resumes or replays an interrupted PUT.
func (f *Local) recoverReceipt(ctx context.Context) error {
	record, err := f.readReceiptFile(pendingReceipt)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	terminal, err := f.readReceiptFile(receiptName(record.ID))
	if err == nil {
		if terminal.Fingerprint != record.Fingerprint {
			return fmt.Errorf("upload journal identity mismatch")
		}
		if err := f.journal.Remove(pendingReceipt); err != nil {
			return err
		}
		return syncRootDirectory(f.journal, ".")
	}
	if !os.IsNotExist(err) {
		return err
	}
	state, status := "interrupted", http.StatusRequestTimeout
	if record.Temporary != "" && !strings.HasPrefix(filepath.Base(record.Temporary), uploadPrefix) {
		return fmt.Errorf("invalid upload staging name")
	}
	if record.State == "receiving" && record.Temporary != "" {
		if err := f.root.Remove(record.Temporary); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if record.State == "prepared" {
		if filepath.Base(record.Temporary) == "." || !strings.HasPrefix(filepath.Base(record.Temporary), uploadPrefix) {
			return fmt.Errorf("invalid prepared upload")
		}
		_, tempErr := f.root.Lstat(record.Temporary)
		if tempErr == nil {
			if err := f.root.Remove(record.Temporary); err != nil {
				return err
			}
		} else if os.IsNotExist(tempErr) {
			state, status = "uncertain", http.StatusServiceUnavailable
			// Recovery belongs to the owner journal. A newly restricted reader
			// must not rewrite an earlier outcome as uncertain merely because
			// its current path policy no longer permits viewing that receipt.
			file, openErr := f.root.OpenFile(record.Target, os.O_RDONLY|nonblockFlag, 0)
			if openErr != nil && !os.IsNotExist(openErr) {
				return openErr
			}
			if openErr == nil {
				info, statErr := file.Stat()
				if statErr != nil {
					file.Close()
					return statErr
				}
				if !info.Mode().IsRegular() {
					file.Close()
					return os.ErrPermission
				}
				if statErr == nil {
					revision, hashErr := contentRevision(ctx, file, info)
					if hashErr != nil {
						file.Close()
						return hashErr
					}
					identity, identityErr := uploadFileIdentity(info)
					if identityErr == nil && identity == record.FileIdentity && revision == `"sha256-`+record.SHA256+`"` && strconv.FormatInt(info.Size(), 10) == record.Size {
						if err := f.syncDirectory(filepath.Dir(record.Target)); err != nil {
							file.Close()
							return err
						}
						state, status = "committed", http.StatusCreated
					}
				}
				file.Close()
			}
		} else {
			return tempErr
		}
	} else if record.State != "receiving" {
		return fmt.Errorf("unknown upload journal state")
	}
	return f.completeReceipt(record, state, status)
}

func writeUploadReceipt(w http.ResponseWriter, record *receiptRecord) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Upload-Id", record.ID)
	w.Header().Set("Cache-Control", "no-store")
	if record.Revision != "" {
		w.Header().Set("ETag", record.Revision)
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(record.UploadReceipt)
}

func (f *Local) serveReceipt(w http.ResponseWriter, r *http.Request, prefix string) {
	if r.URL.Path != prefix && !strings.HasPrefix(r.URL.Path, prefix+"/") {
		http.NotFound(w, r)
		return
	}
	id := r.URL.Query().Get("upload-receipt")
	if !validUploadID(id) {
		http.Error(w, "Invalid upload receipt identity", http.StatusBadRequest)
		return
	}
	name, err := f.lexical(strings.TrimPrefix(r.URL.Path, prefix))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// A live writer may retain the lock while streaming. Reading its atomically
	// replaced journal is safe; only a lock owner may reconcile an old attempt.
	lock, err := f.openMutationLock()
	if err != nil {
		http.Error(w, "Receipt recovery unavailable", http.StatusServiceUnavailable)
		return
	}
	defer lock.Close()
	acquired, err := tryMutationLock(lock)
	if err != nil {
		http.Error(w, "Receipt recovery unavailable", http.StatusServiceUnavailable)
		return
	}
	if acquired {
		defer unlockMutation(lock)
		if err := f.recoverReceipt(r.Context()); err != nil {
			http.Error(w, "Receipt recovery unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	record, err := f.readReceiptFile(receiptName(id))
	if os.IsNotExist(err) {
		record, err = f.readReceiptFile(pendingReceipt)
		if err == nil && record.ID != id {
			err = os.ErrNotExist
		}
		if os.IsNotExist(err) {
			record, err = f.readReceiptFile(receiptName(id))
		}
	}
	if os.IsNotExist(err) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Receipt unavailable", http.StatusServiceUnavailable)
		return
	}
	if record.Path != filepath.ToSlash(name) {
		http.NotFound(w, r)
		return
	}
	if _, err := f.lexical(record.Path); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := f.lexical(record.Target); err != nil {
		http.NotFound(w, r)
		return
	}
	writeUploadReceipt(w, record)
}
