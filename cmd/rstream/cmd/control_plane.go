// See LICENSE file in the project root for license information.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func validateToken(ctx context.Context, apiURL, token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("token is required")
	}
	apiURL = strings.TrimRight(strings.TrimSpace(apiURL), "/")
	if apiURL == "" {
		return errors.New("apiUrl is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"/api/auth", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("token validation failed: %s", resp.Status)
	}
	return nil
}
