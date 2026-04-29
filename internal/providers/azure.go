package providers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
)

// AzureProvider reads blobs from Azure Blob Storage.
// Credentials are resolved automatically via DefaultAzureCredential:
//
//	AZURE_CLIENT_ID, AZURE_TENANT_ID, AZURE_CLIENT_SECRET (service principal)
//	AZURE_CLIENT_CERTIFICATE_PATH                          (cert-based SP)
//	Managed Identity / Azure CLI / environment — all supported automatically
type AzureProvider struct{}

func NewAzure() *AzureProvider {
	return &AzureProvider{}
}

// Fetch downloads a blob from Azure Blob Storage.
// source format: azure://storageaccount/container/path/to/blob
func (p *AzureProvider) Fetch(source string) ([]byte, error) {
	path := strings.TrimPrefix(source, "azure://")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("invalid Azure source %q, expected azure://storageaccount/container/blob", source)
	}
	account, container, blobName := parts[0], parts[1], parts[2]

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("creating Azure credential: %w", err)
	}

	serviceURL := fmt.Sprintf("https://%s.blob.core.windows.net", account)
	client, err := azblob.NewClient(serviceURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("creating Azure Blob client: %w", err)
	}

	resp, err := client.DownloadStream(context.Background(), container, blobName, nil)
	if err != nil {
		return nil, fmt.Errorf("fetching azure://%s/%s/%s: %w", account, container, blobName, err)
	}
	defer resp.Body.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		return nil, fmt.Errorf("reading Azure Blob response body: %w", err)
	}
	return buf.Bytes(), nil
}
