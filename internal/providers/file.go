package providers

import (
	"os"
	"strings"
)

type FileProvider struct{}

func NewFile() *FileProvider {
	return &FileProvider{}
}

func (p *FileProvider) Fetch(source string) ([]byte, error) {
	path := strings.TrimPrefix(source, "file://")
	return os.ReadFile(path)
}
