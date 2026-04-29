package providers

import (
	"strings"
)

type Provider interface {
	Fetch(source string) ([]byte, error)
}

func GetProvider(source string) Provider {
	switch {
	case strings.HasPrefix(source, "http://"), strings.HasPrefix(source, "https://"):
		return NewHTTP()
	case strings.HasPrefix(source, "file://"):
		return NewFile()
	case strings.HasPrefix(source, "s3://"):
		return NewS3()
	case strings.HasPrefix(source, "azure://"):
		return NewAzure()
	default:
		// Treat bare paths (e.g. report.json or /tmp/report.json) as local files
		return NewFile()
	}
}

func Validate(source string) (Provider, error) {
	t := GetProvider(source)
	return t, nil
}
