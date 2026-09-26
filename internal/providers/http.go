package providers

import (
	"fmt"
	"io"
	"net/http"
)

type HTTPProvider struct{}

func NewHTTP() *HTTPProvider {
	return &HTTPProvider{}
}

func (p *HTTPProvider) Fetch(source string) ([]byte, error) {
	resp, err := http.Get(source)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// A 404 ("report not found", "report not available yet") or a 502 from the operator's reports
	// server is not a report; without this its text body would surface as a confusing "Invalid JSON".
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("%s: HTTP %d", source, resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
