// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/webtty"
	"github.com/spf13/cobra"
)

type webTTYFSClient struct {
	client    *http.Client
	baseURL   string
	authToken *string
}

type webTTYFSItem struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Size     *int64 `json:"size,omitempty"`
	Modified string `json:"modified,omitempty"`
}

type webDAVMultiStatus struct {
	Responses []webDAVResponse `xml:"response"`
}

type webDAVResponse struct {
	Href     string           `xml:"href"`
	Propstat []webDAVPropstat `xml:"propstat"`
}

type webDAVPropstat struct {
	Prop   webDAVProp `xml:"prop"`
	Status string     `xml:"status"`
}

type webDAVProp struct {
	ResourceType  webDAVResourceType `xml:"resourcetype"`
	ContentLength string             `xml:"getcontentlength"`
	LastModified  string             `xml:"getlastmodified"`
}

type webDAVResourceType struct {
	Collection *struct{} `xml:"collection"`
}

var webttyFSCmd = &cobra.Command{
	Use:          "fs",
	Short:        "Access a WebTTY filesystem sidecar",
	Long:         "Access a WebTTY filesystem sidecar. Paths are relative to the server --fs-root: / is the configured filesystem root, not necessarily the remote host filesystem root.",
	GroupID:      "webtty-connect",
	SilenceUsage: true,
	Args:         cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var webttyFSListCmd = &cobra.Command{
	Use:          "ls [path]",
	Aliases:      []string{"list"},
	Short:        "List a WebTTY filesystem directory",
	SilenceUsage: true,
	Args:         cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		remotePath := "/"
		if len(args) > 0 {
			remotePath = args[0]
		}
		client, err := newWebTTYFSClient(cmd)
		if err != nil {
			return err
		}
		items, err := client.list(cmd.Context(), remotePath)
		if err != nil {
			return err
		}
		output, _ := cmd.Flags().GetString("output")
		switch strings.ToLower(strings.TrimSpace(output)) {
		case "json":
			return writeStructuredOutput("json", items)
		case "table":
			return printWebTTYFSItemsTable(os.Stdout, items)
		default:
			return fmt.Errorf("invalid --output %q (valid: table, json)", output)
		}
	},
}

var webttyFSReadCmd = &cobra.Command{
	Use:          "read path",
	Short:        "Read a file from a WebTTY filesystem sidecar",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newWebTTYFSClient(cmd)
		if err != nil {
			return err
		}
		return client.read(cmd.Context(), args[0], os.Stdout)
	},
}

var webttyFSWriteCmd = &cobra.Command{
	Use:          "write path",
	Short:        "Write stdin to a WebTTY filesystem sidecar file",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newWebTTYFSClient(cmd)
		if err != nil {
			return err
		}
		return client.write(cmd.Context(), args[0], os.Stdin)
	},
}

var webttyFSUploadCmd = &cobra.Command{
	Use:          "upload local-path remote-path",
	Short:        "Upload a file to a WebTTY filesystem sidecar",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newWebTTYFSClient(cmd)
		if err != nil {
			return err
		}
		file, err := os.Open(args[0])
		if err != nil {
			return fmt.Errorf("failed to open local file: %w", err)
		}
		defer file.Close()
		return client.write(cmd.Context(), args[1], file)
	},
}

var webttyFSDownloadCmd = &cobra.Command{
	Use:          "download remote-path local-path",
	Short:        "Download a file from a WebTTY filesystem sidecar",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newWebTTYFSClient(cmd)
		if err != nil {
			return err
		}
		if args[1] == "-" {
			return client.read(cmd.Context(), args[0], os.Stdout)
		}
		file, err := os.Create(args[1])
		if err != nil {
			return fmt.Errorf("failed to create local file: %w", err)
		}
		defer file.Close()
		return client.read(cmd.Context(), args[0], file)
	},
}

var webttyFSMkdirCmd = &cobra.Command{
	Use:          "mkdir path",
	Short:        "Create a directory in a WebTTY filesystem sidecar",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newWebTTYFSClient(cmd)
		if err != nil {
			return err
		}
		return client.mkcol(cmd.Context(), args[0])
	},
}

var webttyFSRemoveCmd = &cobra.Command{
	Use:          "rm path",
	Aliases:      []string{"remove"},
	Short:        "Remove a file or directory from a WebTTY filesystem sidecar",
	SilenceUsage: true,
	Args:         cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newWebTTYFSClient(cmd)
		if err != nil {
			return err
		}
		return client.delete(cmd.Context(), args[0])
	},
}

func init() {
	webttyFSCmd.Flags().SortFlags = false
	webttyFSCmd.PersistentFlags().SortFlags = false
	webttyFSCmd.PersistentFlags().String("url", "ws://127.0.0.1:8080", "WebTTY server URL (http://, https://, ws://, wss://, or rstrm://<tunnel-id-or-name>)")
	webttyFSCmd.PersistentFlags().String("fs-path", "", "advertised WebTTY filesystem sidecar path")
	webttyFSCmd.PersistentFlags().String("auth-token-file", "", "read local WebTTY bearer token from file")
	webttyFSListCmd.Flags().StringP("output", "o", "table", "output mode (table, json)")
	webttyFSCmd.AddCommand(webttyFSListCmd)
	webttyFSCmd.AddCommand(webttyFSReadCmd)
	webttyFSCmd.AddCommand(webttyFSWriteCmd)
	webttyFSCmd.AddCommand(webttyFSUploadCmd)
	webttyFSCmd.AddCommand(webttyFSDownloadCmd)
	webttyFSCmd.AddCommand(webttyFSMkdirCmd)
	webttyFSCmd.AddCommand(webttyFSRemoveCmd)
	webttyCmd.AddCommand(webttyFSCmd)
}

func newWebTTYFSClient(cmd *cobra.Command) (*webTTYFSClient, error) {
	rawURL, _ := cmd.Flags().GetString("url")
	fsPath, _ := cmd.Flags().GetString("fs-path")
	baseURL, target, err := resolveWebTTYFSBaseURL(rawURL, fsPath)
	if err != nil {
		return nil, err
	}
	authToken, err := readWebTTYAuthToken(cmd)
	if err != nil {
		return nil, err
	}
	httpClient := http.DefaultClient
	if target != "" {
		runtime, err := resolveRuntime(cmd, true, true)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve runtime: %w", err)
		}
		client, err := newClientFromResolved(runtime.Resolved)
		if err != nil {
			return nil, fmt.Errorf("failed to create rstream client: %w", err)
		}
		serverInfo, err := resolveWebTTYRuntimeServerInfo(cmd.Context(), client, target)
		if err != nil {
			return nil, err
		}
		if err := validateWebTTYFilesystemCapability(target, serverInfo); err != nil {
			return nil, err
		}
		dialTarget := webTTYRuntimeDialTarget(serverInfo)
		rawURL, err = webTTYURLWithRstreamTarget(rawURL, dialTarget)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(fsPath) == "" && serverInfo.FSPath != nil {
			fsPath = strings.TrimSpace(*serverInfo.FSPath)
		}
		baseURL, target, err = resolveWebTTYFSBaseURL(rawURL, fsPath)
		if err != nil {
			return nil, err
		}
		httpClient = &http.Client{Transport: &http.Transport{DialContext: newWebTTYFSDialContext(client, target)}}
	}
	return &webTTYFSClient{client: httpClient, baseURL: baseURL, authToken: authToken}, nil
}

func validateWebTTYFilesystemCapability(target string, serverInfo *webtty.ServerInfo) error {
	if serverInfo == nil {
		return fmt.Errorf("WebTTY server %q is not online", target)
	}
	for _, capability := range serverInfo.Capabilities {
		if capability == webtty.WebTTYCapabilityFS {
			return nil
		}
	}
	return fmt.Errorf("WebTTY server %q does not advertise a filesystem sidecar", target)
}

func resolveWebTTYFSBaseURL(raw string, fsPath string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "ws://127.0.0.1:8080"
	}
	sidecarInput := fsPath
	if strings.TrimSpace(sidecarInput) == "" {
		sidecarInput = webtty.WebTTYDefaultFSPath
	}
	sidecarPath, err := normalizeWebTTYEndpointPath(sidecarInput)
	if err != nil {
		return "", "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid WebTTY filesystem URL: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "rstrm":
		target := strings.TrimSpace(u.Host)
		if target == "" {
			return "", "", fmt.Errorf("rstrm URL is missing tunnel id or name")
		}
		if strings.TrimSpace(fsPath) == "" && strings.Trim(strings.TrimSpace(u.Path), "/") != "" {
			sidecarPath = path.Clean(u.Path)
		}
		return "http://" + target + sidecarPath, target, nil
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return "", "", fmt.Errorf("unsupported WebTTY filesystem URL scheme %q", u.Scheme)
	}
	basePath := strings.TrimRight(u.Path, "/")
	if basePath == "" || basePath == "/" {
		u.Path = sidecarPath
	} else if basePath != sidecarPath && !strings.HasPrefix(basePath, sidecarPath+"/") {
		u.Path = path.Join(basePath, sidecarPath)
	}
	u.Fragment = ""
	return u.String(), "", nil
}

func newWebTTYFSDialContext(client *rstream.Client, target string) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return client.Dial(ctx, rstream.Addr{IdOrName: target})
	}
}

func (c *webTTYFSClient) list(ctx context.Context, remotePath string) ([]webTTYFSItem, error) {
	body := strings.NewReader(`<?xml version="1.0" encoding="utf-8"?><propfind xmlns="DAV:"><prop><resourcetype/><getcontentlength/><getlastmodified/></prop></propfind>`)
	req, err := c.newRequest(ctx, "PROPFIND", remotePath, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Depth", "1")
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webtty filesystem list failed: %w", err)
	}
	defer resp.Body.Close()
	if err := requireWebTTYFSSuccess(resp, http.StatusMultiStatus); err != nil {
		return nil, err
	}
	var multi webDAVMultiStatus
	if err := xml.NewDecoder(resp.Body).Decode(&multi); err != nil {
		return nil, fmt.Errorf("failed to decode WebDAV response: %w", err)
	}
	return webDAVItemsFromMultiStatus(multi), nil
}

func (c *webTTYFSClient) read(ctx context.Context, remotePath string, w io.Writer) error {
	req, err := c.newRequest(ctx, http.MethodGet, remotePath, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("webtty filesystem read failed: %w", err)
	}
	defer resp.Body.Close()
	if err := requireWebTTYFSSuccess(resp, http.StatusOK); err != nil {
		return err
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

func (c *webTTYFSClient) write(ctx context.Context, remotePath string, r io.Reader) error {
	req, err := c.newRequest(ctx, http.MethodPut, remotePath, r)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("webtty filesystem write failed: %w", err)
	}
	defer resp.Body.Close()
	return requireWebTTYFSSuccess(resp, http.StatusCreated, http.StatusNoContent, http.StatusOK)
}

func (c *webTTYFSClient) mkcol(ctx context.Context, remotePath string) error {
	req, err := c.newRequest(ctx, "MKCOL", remotePath, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("webtty filesystem mkdir failed: %w", err)
	}
	defer resp.Body.Close()
	return requireWebTTYFSSuccess(resp, http.StatusCreated, http.StatusMethodNotAllowed)
}

func (c *webTTYFSClient) delete(ctx context.Context, remotePath string) error {
	req, err := c.newRequest(ctx, http.MethodDelete, remotePath, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("webtty filesystem remove failed: %w", err)
	}
	defer resp.Body.Close()
	return requireWebTTYFSSuccess(resp, http.StatusNoContent, http.StatusOK)
}

func (c *webTTYFSClient) newRequest(ctx context.Context, method string, remotePath string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.remoteURL(remotePath), body)
	if err != nil {
		return nil, err
	}
	if c.authToken != nil && strings.TrimSpace(*c.authToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(*c.authToken))
	}
	return req, nil
}

func (c *webTTYFSClient) remoteURL(remotePath string) string {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return strings.TrimRight(c.baseURL, "/") + normalizeWebTTYFSPath(remotePath)
	}
	base.Path = path.Join(base.Path, normalizeWebTTYFSPath(remotePath))
	return base.String()
}

func normalizeWebTTYFSPath(remotePath string) string {
	remotePath = strings.TrimSpace(remotePath)
	if remotePath == "" {
		return "/"
	}
	if !strings.HasPrefix(remotePath, "/") {
		remotePath = "/" + remotePath
	}
	cleaned := path.Clean(remotePath)
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func requireWebTTYFSSuccess(resp *http.Response, allowed ...int) error {
	for _, status := range allowed {
		if resp.StatusCode == status {
			return nil
		}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = resp.Status
	}
	return fmt.Errorf("webtty filesystem request failed with status %s: %s", resp.Status, message)
}

func webDAVItemsFromMultiStatus(multi webDAVMultiStatus) []webTTYFSItem {
	items := make([]webTTYFSItem, 0, len(multi.Responses))
	for _, response := range multi.Responses {
		item := webDAVItemFromResponse(response)
		if item.Path != "" {
			items = append(items, item)
		}
	}
	return items
}

func webDAVItemFromResponse(response webDAVResponse) webTTYFSItem {
	prop := webDAVOKProp(response.Propstat)
	itemPath := webDAVDisplayPath(response.Href)
	kind := "file"
	if prop.ResourceType.Collection != nil {
		kind = "directory"
	}
	var size *int64
	if strings.TrimSpace(prop.ContentLength) != "" {
		if parsed, err := strconv.ParseInt(strings.TrimSpace(prop.ContentLength), 10, 64); err == nil {
			size = &parsed
		}
	}
	return webTTYFSItem{Path: itemPath, Kind: kind, Size: size, Modified: strings.TrimSpace(prop.LastModified)}
}

func webDAVOKProp(props []webDAVPropstat) webDAVProp {
	for _, propstat := range props {
		if strings.Contains(propstat.Status, " 200 ") {
			return propstat.Prop
		}
	}
	return webDAVProp{}
}

func webDAVDisplayPath(raw string) string {
	value, err := url.PathUnescape(raw)
	if err == nil {
		raw = value
	}
	raw = strings.TrimPrefix(raw, webtty.WebTTYDefaultFSPath)
	if raw == "" {
		return "/"
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return raw
}

func printWebTTYFSItemsTable(w io.Writer, items []webTTYFSItem) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "PATH\tKIND\tSIZE\tMODIFIED")
	for _, item := range items {
		size := "-"
		if item.Size != nil {
			size = strconv.FormatInt(*item.Size, 10)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", item.Path, item.Kind, size, terminalSafeDefault(item.Modified))
	}
	return tw.Flush()
}
