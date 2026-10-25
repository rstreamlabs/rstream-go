// See LICENSE file in the project root for license information.

package filesystem

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const stateDirectory = uploadPrefix + "state"
const journalOwnerName = "owner.json"

var errReceiptCapacity = errors.New("upload receipt capacity reached")

type journalOwner struct {
	Version  int    `json:"version"`
	Root     string `json:"root"`
	Identity string `json:"identity"`
}

func privateUploadPath(name string) bool {
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if strings.HasPrefix(strings.ToLower(part), uploadPrefix) {
			return true
		}
	}
	return false
}

// StateDir is an existing, persistent directory dedicated to this export.
// Always use a reserved child name: another Files instance exporting an ancestor
// must also hide it, even with IncludeHidden or after the owner has stopped.
func (f *Local) openJournal(name string) error {
	if !durableUploads {
		return fmt.Errorf("external upload state is not supported on this platform")
	}
	for _, old := range []string{receiptDirectory, pendingReceipt} {
		if _, err := f.root.Lstat(old); err == nil {
			return fmt.Errorf("existing upload journal at export root; keep its location until an offline migration is performed")
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	parent, err := os.OpenRoot(name)
	if err != nil {
		return err
	}
	defer parent.Close()
	if !f.policy.ReadOnly {
		if err := parent.Mkdir(stateDirectory, 0o700); err != nil && !os.IsExist(err) {
			return err
		}
	}
	info, err := parent.Lstat(stateDirectory)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("private upload state must be a directory accessible only to its owner")
	}
	journal, err := parent.OpenRoot(stateDirectory)
	if err != nil {
		return err
	}
	f.journal = journal
	lock, err := journal.Open(".")
	if err != nil {
		return err
	}
	opened, err := lock.Stat()
	if err != nil || !os.SameFile(info, opened) {
		lock.Close()
		return os.ErrPermission
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := lockMutationFile(ctx, lock)
	if err != nil {
		return err
	}
	defer release()
	rootInfo, err := f.root.Stat(".")
	if err != nil {
		return err
	}
	identity, err := uploadFileIdentity(rootInfo)
	if err != nil {
		return err
	}
	owner := journalOwner{Version: 1, Root: f.path, Identity: identity}
	data, err := readJournalFile(journal, journalOwnerName)
	if os.IsNotExist(err) && !f.policy.ReadOnly {
		// Never adopt unbound state: it may contain receipts from another export.
		entries, readErr := lock.Readdirnames(1)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if len(entries) != 0 {
			return fmt.Errorf("upload state has no owner; refusing to adopt existing state")
		}
		data, err = json.Marshal(owner)
		if err == nil {
			err = writeJournalFile(journal, journalOwnerName, data)
		}
	} else if err == nil {
		var stored journalOwner
		if err := json.Unmarshal(data, &stored); err != nil {
			return fmt.Errorf("invalid upload state owner: %w", err)
		}
		// Canonical paths keep symlinked aliases together. A relocated or
		// replaced export, or changed mount identity, requires explicit review.
		if stored.Version != owner.Version || stored.Root != owner.Root || stored.Identity != owner.Identity {
			return fmt.Errorf("upload state belongs to a different export; refusing to reuse its receipts")
		}
	}
	if err != nil {
		return err
	}
	if !f.policy.ReadOnly {
		receipts, err := f.openReceiptDirectory()
		if err != nil {
			return err
		}
		if err := errors.Join(probeJournal(receipts), receipts.Close()); err != nil {
			return err
		}
		if err := probeJournal(journal); err != nil {
			return err
		}
		return syncRootDirectory(parent, ".")
	}
	return nil
}

func readJournalFile(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<10 {
		return nil, os.ErrPermission
	}
	file, err := root.OpenFile(name, os.O_RDONLY|nonblockFlag, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, os.ErrPermission
	}
	return io.ReadAll(io.LimitReader(file, 16<<10))
}

func writeJournalFile(root *os.Root, name string, data []byte) error {
	temporary := uploadPrefix + "journal-" + rand.Text()
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	if err := root.Rename(temporary, name); err != nil {
		return err
	}
	return syncRootDirectory(root, filepath.Dir(name))
}

func probeJournal(root *os.Root) error {
	name := uploadPrefix + "probe-" + rand.Text()
	if err := writeJournalFile(root, name, nil); err != nil {
		return err
	}
	if err := root.Remove(name); err != nil {
		return err
	}
	return syncRootDirectory(root, ".")
}

func journalError(err error) (int, string) {
	if errors.Is(err, errReceiptCapacity) || errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.EDQUOT) {
		return http.StatusInsufficientStorage, "Upload journal capacity exhausted"
	}
	if errors.Is(err, os.ErrPermission) {
		return http.StatusServiceUnavailable, "Upload journal is not writable"
	}
	return http.StatusServiceUnavailable, "Upload journal unavailable"
}
