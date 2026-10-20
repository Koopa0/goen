//go:build integration

package google

import "net/http"

// SetHTTPClient lets other packages' integration tests answer Google's two
// HTTP endpoints without making transport injection part of the production API.
func SetHTTPClient(c *Client, client *http.Client) {
	c.http = client
}
