package providers

import (
	"io"
	"net/http"
)

type HTTProvider struct{}

func NewHTTP() *HTTProvider {
	return &HTTProvider{}
}

func (p *HTTProvider) Fetch(source string) ([]byte, error) {
	resp, err := http.Get(source)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}
