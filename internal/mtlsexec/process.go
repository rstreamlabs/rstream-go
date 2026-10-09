// See LICENSE file in the project root for license information.

package mtlsexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func (p *Provider) run(parent context.Context, request Request) (Response, error) {
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	select {
	case p.gate <- struct{}{}:
		defer func() { <-p.gate }()
	case <-ctx.Done():
		return Response{}, fmt.Errorf("external mTLS %s: %w", request.Operation, ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		return Response{}, fmt.Errorf("external mTLS %s: %w", request.Operation, err)
	}
	info, err := os.Stat(p.options.Command)
	if err != nil || !info.Mode().IsRegular() {
		return Response{}, errors.New("external mTLS executable is unavailable or is not a regular file")
	}
	if runtime.GOOS != "windows" && (info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0) {
		return Response{}, errors.New("external mTLS executable is not executable or is writable by other users")
	}
	request.Version, request.CertificateSHA256 = Version, p.fingerprint
	data, err := json.Marshal(request)
	if err != nil || len(data) > MaxRequestBytes {
		return Response{}, errors.New("external mTLS request exceeds limit")
	}
	cmd := exec.CommandContext(ctx, p.options.Command, p.options.Args...)
	cmd.Env, err = helperEnvironment(p.options.PassEnv)
	if err != nil {
		return Response{}, err
	}
	cmd.Stdin = bytes.NewReader(data)
	cmd.WaitDelay = 250 * time.Millisecond
	stdout := &limitedOutput{limit: MaxResponseBytes, cancel: cancel}
	stderr := &limitedOutput{limit: 4096, cancel: cancel, discard: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cleanup := configureProcess(cmd)
	defer cleanup()
	runErr := cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return Response{}, errors.New("external mTLS helper output exceeds limit")
	}
	if err := ctx.Err(); err != nil {
		return Response{}, fmt.Errorf("external mTLS %s: %w", request.Operation, err)
	}
	if runErr != nil {
		var exit *exec.ExitError
		if errors.As(runErr, &exit) {
			return Response{}, fmt.Errorf("external mTLS %s helper exited with status %d", request.Operation, exit.ExitCode())
		}
		return Response{}, fmt.Errorf("external mTLS %s helper could not execute or complete", request.Operation)
	}
	var response Response
	decoder := json.NewDecoder(bytes.NewReader(stdout.data.Bytes()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return Response{}, errors.New("external mTLS helper returned invalid JSON")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Response{}, errors.New("external mTLS helper returned trailing data")
	}
	if response.Version != Version {
		return Response{}, errors.New("external mTLS helper returned an unsupported protocol version")
	}
	if response.Error != "" {
		switch response.Error {
		case "unsupported_version", "unsupported_algorithm", "identity_unavailable", "identity_mismatch", "signing_denied", "invalid_request", "internal_error":
			return Response{}, fmt.Errorf("external mTLS %s: %s", request.Operation, response.Error)
		default:
			return Response{}, errors.New("external mTLS helper returned an unknown error code")
		}
	}
	return response, nil
}

func helperEnvironment(pass []string) ([]string, error) {
	env := []string{"LANG=C", "LC_ALL=C"}
	if runtime.GOOS == "windows" {
		if root := os.Getenv("SystemRoot"); root != "" {
			env = append(env, "SystemRoot="+root)
		}
	}
	size := 0
	for _, name := range pass {
		value, ok := os.LookupEnv(name)
		if !ok {
			return nil, errors.New("external mTLS passEnv variable is not set")
		}
		size += len(name) + len(value)
		if size > 64<<10 || strings.ContainsRune(value, 0) {
			return nil, errors.New("external mTLS environment is invalid or exceeds limit")
		}
		env = append(env, name+"="+value)
	}
	return env, nil
}

type limitedOutput struct {
	data     bytes.Buffer
	count    int
	limit    int
	exceeded bool
	discard  bool
	cancel   context.CancelFunc
}

func (w *limitedOutput) Write(data []byte) (int, error) {
	if len(data) > w.limit-w.count {
		w.exceeded = true
		w.cancel()
		return 0, errors.New("external mTLS output exceeds limit")
	}
	w.count += len(data)
	if w.discard {
		return len(data), nil
	}
	return w.data.Write(data)
}
