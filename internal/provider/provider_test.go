package provider

import (
	"context"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
)

func TestMetadata(t *testing.T) {
	var resp fwprovider.MetadataResponse
	New("1.2.3")().Metadata(context.Background(), fwprovider.MetadataRequest{}, &resp)
	if resp.TypeName != "ligo" || resp.Version != "1.2.3" {
		t.Fatalf("got %q %q, want ligo 1.2.3", resp.TypeName, resp.Version)
	}
}
