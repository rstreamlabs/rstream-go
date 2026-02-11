// See LICENSE file in the project root for license information.

package controlplane

type Whoami struct {
	ID          string   `json:"id"`
	Role        string   `json:"role"`
	Name        string   `json:"name,omitempty"`
	Email       string   `json:"email,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

type Project struct {
	ID          string `json:"id"`
	WorkspaceID string `json:"workspaceId"`
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	URL         string `json:"url"`
	Status      string `json:"status"`
	Provider    string `json:"provider"`
	Region      string `json:"region,omitempty"`
	Plan        string `json:"plan"`
	Deployment  string `json:"deployment"`
}

type ListProjectsResponse struct {
	Projects   []Project `json:"projects"`
	Page       int       `json:"page"`
	PageSize   int       `json:"pageSize"`
	Total      int       `json:"total"`
	TotalPages int       `json:"totalPages"`
}

type ListProjectsParams struct {
	Query    string
	Page     *int
	PageSize *int
	Sort     string
	Order    string
}
