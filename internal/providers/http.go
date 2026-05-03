package providers

import (
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

	return io.ReadAll(resp.Body)
}
