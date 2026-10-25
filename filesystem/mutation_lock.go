// See LICENSE file in the project root for license information.

package filesystem

import (
	"context"
	"os"
	"time"
)

const mutationLockName = uploadPrefix + "mutation-lock"

// Each acquisition owns its descriptor: separate Local instances and processes
// must coordinate the precondition and replacement in the same exported root.
// The OS releases this advisory lock when its process exits. Native writers
// outside this service must follow the same namespace ownership discipline.
func (f *Local) lockMutation(ctx context.Context) (func(), error) {
	file, err := f.openMutationLock()
	if err != nil {
		return nil, err
	}
	return lockMutationFile(ctx, file)
}

// lockMutationFile takes ownership of file, including on failure.
func lockMutationFile(ctx context.Context, file *os.File) (func(), error) {
	timer := time.NewTicker(20 * time.Millisecond)
	defer timer.Stop()
	for {
		if err := ctx.Err(); err != nil {
			file.Close()
			return nil, err
		}
		acquired, err := tryMutationLock(file)
		if err != nil {
			file.Close()
			return nil, err
		}
		if acquired {
			return func() { unlockMutation(file); file.Close() }, nil
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
