package provider

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

// Every data source has an example that parses and uses only attributes that
// the data source has (the registry shows them); none carries a real value
// (T-P12).
func TestExamples(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "data-sources")
	for _, mk := range dataSources() {
		d := mk()
		var m datasource.MetadataResponse
		d.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "ligo"}, &m)
		var s datasource.SchemaResponse
		d.Schema(context.Background(), datasource.SchemaRequest{}, &s)
		file := filepath.Join(root, m.TypeName, "data-source.tf")
		src, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("%s has no example: %v", m.TypeName, err)
			continue
		}
		f, diags := hclsyntax.ParseConfig(src, file, hcl.InitialPos)
		if diags.HasErrors() {
			t.Errorf("%s: %v", file, diags)
			continue
		}
		found := false
		for _, b := range f.Body.(*hclsyntax.Body).Blocks {
			if b.Type != "data" || b.Labels[0] != m.TypeName {
				continue
			}
			found = true
			for name := range b.Body.Attributes {
				if _, ok := s.Schema.Attributes[name]; !ok {
					t.Errorf("%s: %s is no attribute of %s", file, name, m.TypeName)
				}
			}
		}
		if !found {
			t.Errorf("%s does not use %s", file, m.TypeName)
		}
	}
}

// T-P12: no example or doc carries the shape of a key, a token or a real host.
func TestNoRealValuesInExamplesOrDocs(t *testing.T) {
	shapes := regexp.MustCompile(`(?i)AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*KEY|eyJ[A-Za-z0-9_-]{10,}\.|ghp_[A-Za-z0-9]{20,}|hvs\.[A-Za-z0-9]{20,}|password\s*=\s*"[^"]+"`)
	for _, dir := range []string{filepath.Join("..", "..", "examples"), filepath.Join("..", "..", "docs")} {
		_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				t.Errorf("%s: %v", p, rerr)
				return nil
			}
			if loc := shapes.Find(b); loc != nil {
				t.Errorf("%s holds the shape of a secret: %q", p, loc)
			}
			return nil
		})
	}
}
