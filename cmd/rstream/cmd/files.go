// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/fileserver"
	filesui "github.com/rstreamlabs/rstream-go/fileserver/ui"
	"github.com/rstreamlabs/rstream-go/filesystem"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newFilesCmd() *cobra.Command {
	cmd := &cobra.Command{
		GroupID: "common", Use: "files [path]", Short: "Share a file or directory through an HTTPS tunnel",
		Long:    "Share a file or directory in read-only mode by default. Use --read-write to enable WebDAV writes and --no-ui to serve only transfer endpoints. The default path is the current directory. Hidden files are excluded unless explicitly included. Access is public unless authentication is selected or required by the project.",
		Example: "  rstream files ./exports\n  rstream files ./backup.tar.zst --password\n  rstream files ./exports --rstream-auth",
		Args:    cobra.MaximumNArgs(1), SilenceUsage: true, RunE: runFiles,
	}
	cmd.Flags().SortFlags = false
	cmd.Flags().StringP("output", "o", "", "output mode (text, json, xterm, none)")
	cmd.Flags().String("name", "", "tunnel name")
	cmd.Flags().String("host", "", "Stable domain for publishing")
	cmd.Flags().StringArray("label", nil, "set tunnel labels (key=value)")
	cmd.Flags().String("backend", filesystem.BackendWebDAV, "file transfer backend (webdav, webrtc; WebRTC is read-only)")
	cmd.Flags().Bool("read-write", false, "allow WebDAV uploads and filesystem changes")
	cmd.Flags().Int64("max-upload-size", fileserver.DefaultMaxUploadSize, "maximum WebDAV upload size in bytes (requires --read-write)")
	cmd.Flags().Bool("no-ui", false, "disable the embedded web page; keep transfer and discovery endpoints")
	cmd.Flags().Bool("include-hidden", false, "include hidden files and directories")
	cmd.Flags().StringArray("exclude", nil, "exclude a basename or root-relative glob (repeatable)")
	cmd.Flags().Bool("password", false, "prompt for an HTTP Basic password")
	cmd.Flags().String("password-file", "", "read the password from a file, or - for stdin")
	cmd.Flags().String("username", "rstream", "HTTP Basic username")
	cmd.Flags().Bool("rstream-auth", false, "require rstream account authentication (Pro or Enterprise)")
	cmd.Flags().Bool("token-auth", false, "require an rstream token (for authenticated HTTP clients)")
	cmd.Flags().Bool("retry", true, "enable automatic reconnection on disconnect")
	cmd.Flags().Bool("no-retry", false, "disable automatic reconnection")
	cmd.Flags().Int64("retry-interval", 5000, "retry interval in ms")
	cmd.MarkFlagsMutuallyExclusive("password", "password-file")
	cmd.MarkFlagsMutuallyExclusive("retry", "no-retry")
	return cmd
}

func init() { rootCmd.AddCommand(newFilesCmd()) }

func runFiles(cmd *cobra.Command, args []string) error {
	root := "."
	if len(args) != 0 {
		root = args[0]
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	serviceConfig, err := filesConfig(cmd, root)
	if err != nil {
		return err
	}
	username, _ := cmd.Flags().GetString("username")
	if strings.ContainsAny(username, ":\r\n") || username == "" {
		return fmt.Errorf("--username must be nonempty and contain no colon or newline")
	}
	password, err := filesPassword(cmd, username)
	if err != nil {
		return err
	}
	if password == "" && cmd.Flags().Changed("username") {
		return fmt.Errorf("--username requires --password or --password-file")
	}
	rtcConfig, err := filesystemRTCConfig(cmd, serviceConfig.Backend)
	if err != nil {
		return err
	}
	props := &rstream.TunnelProperties{
		Type:        rstream.TunnelTypePtr(rstream.TunnelTypeBytestream),
		Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
		HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1),
		Publish:     rstream.BoolPtr(true),
		Name:        getStringPtr(cmd, "name"), Hostname: getStringPtr(cmd, "host"),
		TokenAuth: getBoolPtr(cmd, "token-auth"), RstreamAuth: getBoolPtr(cmd, "rstream-auth"),
		Labels: getStringArrayMap(cmd, "label"),
	}
	if password != "" && (boolPtrValue(props.TokenAuth) || boolPtrValue(props.RstreamAuth)) {
		return fmt.Errorf("local password authentication cannot be combined with --token-auth or --rstream-auth")
	}
	s, err := newForwardCtxWithProperties(cmd, "", "", props)
	if err != nil {
		return err
	}
	defer s.Close()
	s.Logger = slog.With("cmd", "files")
	if ui, ok := s.UI.(interface{ SetFilesMode() }); ok {
		ui.SetFilesMode()
	}
	serviceConfig.RTC = rtcConfig
	serviceConfig.Username = username
	serviceConfig.Password = password
	serviceConfig.Logger = s.Logger
	serviceConfig.OnActivity = s.addFileActivity
	service, err := fileserver.New(serviceConfig)
	if err != nil {
		return err
	}
	defer service.Close()
	s.LocalHTTP = &localHTTPService{Server: service, Root: root, Password: password != ""}
	if err := runForwardWithUI(cmd.Context(), s.UI, s.run); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func filesConfig(cmd *cobra.Command, root string) (fileserver.Config, error) {
	cfg := fileserver.Config{Root: root}
	cfg.Backend, _ = cmd.Flags().GetString("backend")
	cfg.ReadWrite, _ = cmd.Flags().GetBool("read-write")
	cfg.IncludeHidden, _ = cmd.Flags().GetBool("include-hidden")
	cfg.Exclude, _ = cmd.Flags().GetStringArray("exclude")
	if cfg.ReadWrite || cmd.Flags().Changed("max-upload-size") {
		cfg.MaxUploadSize, _ = cmd.Flags().GetInt64("max-upload-size")
		if cfg.MaxUploadSize <= 0 {
			return cfg, fmt.Errorf("--max-upload-size must be greater than zero")
		}
	}
	if noUI, _ := cmd.Flags().GetBool("no-ui"); !noUI {
		cfg.UI = filesui.Handler()
	}
	return cfg, cfg.Validate()
}

func filesPassword(cmd *cobra.Command, username string) (string, error) {
	filename, _ := cmd.Flags().GetString("password-file")
	prompt, _ := cmd.Flags().GetBool("password")
	if !prompt && filename == "" {
		if cmd.Flags().Changed("password-file") {
			return "", fmt.Errorf("--password-file cannot be empty")
		}
		return "", nil
	}
	if prompt {
		input, ok := cmd.InOrStdin().(*os.File)
		if !ok || !term.IsTerminal(int(input.Fd())) {
			return "", fmt.Errorf("--password requires a terminal; use --password-file - for stdin")
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "HTTP Basic username: %s\nPassword for rstream files: ", username)
		value, err := readPasswordContext(cmd.Context(), input)
		_, _ = fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", err
		}
		return validateFilesPassword(value)
	}
	var reader io.Reader = cmd.InOrStdin()
	if filename != "-" {
		file, err := os.Open(filename)
		if err != nil {
			return "", fmt.Errorf("read password file: %w", err)
		}
		defer file.Close()
		reader = file
	}
	value, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	return validateFilesPassword([]byte(strings.TrimSuffix(strings.TrimSuffix(string(value), "\n"), "\r")))
}

func validateFilesPassword(value []byte) (string, error) {
	if len(value) == 0 || len(value) > 4096 || strings.ContainsAny(string(value), "\r\n\x00") {
		return "", fmt.Errorf("password must contain 1–4096 bytes and no newline or NUL")
	}
	return string(value), nil
}

type localHTTPService struct {
	Server   *fileserver.Server
	Root     string
	Password bool
}

func (s *forwardCtx) serveLocalHTTP(ctx context.Context, tunnel rstream.Tunnel, props rstream.TunnelProperties, address string, status forwardStatus) error {
	listener, ok := tunnel.(net.Listener)
	if !ok {
		return fmt.Errorf("file sharing requires an HTTP bytestream listener")
	}
	access, err := filesAccess(props, s.LocalHTTP.Password)
	if err != nil {
		return &rstream.EngineError{Code: rstream.EngineErrorCodeInvalidRequest, Message: err.Error()}
	}
	s.LocalHTTP.Server.SetAccess(access)
	info := s.LocalHTTP.Server.Info()
	status.Files = &info
	status.Status = rstream.StringPtr("online")
	status.TunnelID = props.ID
	status.Forwarding = &address
	mode := filesMode(&info)
	if access == "public" {
		access = "public access without authentication"
	}
	description := s.LocalHTTP.Root + " (" + mode + "; " + access + ")"
	status.Forwarded = &description
	s.setStatus(status)
	return serveFilesHTTP(ctx, listener, s.LocalHTTP.Server)
}

func filesAccess(props rstream.TunnelProperties, password bool) (string, error) {
	if password {
		if boolPtrValue(props.TokenAuth) || boolPtrValue(props.RstreamAuth) {
			return "", fmt.Errorf("the project requires edge authentication; remove local password authentication and use --token-auth or --rstream-auth")
		}
		return "password", nil
	}
	if boolPtrValue(props.RstreamAuth) {
		return "rstream", nil
	}
	if boolPtrValue(props.TokenAuth) {
		return "token", nil
	}
	return "public", nil
}

func serveFilesHTTP(ctx context.Context, listener net.Listener, handler http.Handler) error {
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var requests sync.WaitGroup
	var gate sync.Mutex
	stopping := false
	server := &http.Server{
		ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10,
		BaseContext: func(net.Listener) context.Context { return requestCtx },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gate.Lock()
			if stopping {
				gate.Unlock()
				http.Error(w, "File share is stopping", http.StatusServiceUnavailable)
				return
			}
			requests.Add(1)
			gate.Unlock()
			defer requests.Done()
			handler.ServeHTTP(w, r)
		}),
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
		_ = server.Close()
		<-done
	case err = <-done:
	}
	gate.Lock()
	stopping = true
	gate.Unlock()
	cancel()
	shutdownCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if closeErr := server.Shutdown(shutdownCtx); closeErr != nil {
		_ = server.Close()
	}
	requests.Wait()
	if errors.Is(err, http.ErrServerClosed) && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func filesMode(info *fileserver.Info) string {
	if info.Capabilities.Write {
		return "read-write"
	}
	return "read-only"
}
