// Package manuals holds the tests that keep the PDF guides complete and
// aligned with the code (.specs/ligo-terraform-provider.md): the guides are
// Markdown, built to PDF by build.sh.
package manuals

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// walk is the text of the files under dir that have the suffix and, when
// skipTests, are not test files.
func walk(t *testing.T, dir, suffix string, skipTests bool) string {
	t.Helper()
	var all strings.Builder
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, suffix) || (skipTests && strings.HasSuffix(path, "_test.go")) {
			return err
		}
		data, err := os.ReadFile(filepath.Clean(path))
		all.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return all.String()
}

// guides is the text of the three guides.
func guides(t *testing.T) string { return walk(t, ".", ".md", false) }

// TestEveryObjectIsDocumented: each resource and data source of the registry
// reference (generated from the schemas, and checked in CI) and each provider
// attribute is named in the guides.
func TestEveryObjectIsDocumented(t *testing.T) {
	text := guides(t)
	for _, kind := range []string{"resources", "data-sources"} {
		files, err := filepath.Glob(filepath.Join("..", "docs", kind, "*.md"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no %s in the registry reference: %v", kind, err)
		}
		for _, f := range files {
			name := "ligo_" + strings.TrimSuffix(filepath.Base(f), ".md")
			// a data source's plural is named with its singular
			if kind == "data-sources" && strings.HasSuffix(name, "s") && !strings.HasSuffix(name, "ss") {
				continue
			}
			if !strings.Contains(text, name) {
				t.Errorf("the guides do not mention %s", name)
			}
		}
	}
	index := read(t, filepath.Join("..", "docs", "index.md"))
	attrs := regexp.MustCompile("(?m)^- `([a-z_]+)` \\(").FindAllStringSubmatch(index, -1)
	if len(attrs) == 0 {
		t.Fatal("no provider attributes in docs/index.md")
	}
	for _, m := range attrs {
		if !strings.Contains(text, "`"+m[1]+"`") {
			t.Errorf("the guides do not mention the provider attribute %s", m[1])
		}
	}
}

// TestQuotedMessagesAreInTheCode: every message the guides quote is in a
// non-test source of the provider.
func TestQuotedMessagesAreInTheCode(t *testing.T) {
	code := walk(t, filepath.Join("..", "internal"), ".go", true)
	troubleshooting := read(t, "operators-guide/06-troubleshooting.md")
	for _, msg := range []string{
		"A person cannot own a resource",
		"Managed by another automation",
		"A change request is pending",
		"is pending with another content",
		"changed outside Terraform",
		"is held",
		"has an open break-glass change",
		"is still used",
		"ligo refuses the change in its present state",
		"the caller may not file this change",
		"exists in ligo, and this run did not create it",
		"rate limited by ligo after",
		"interrupted while waiting for change request",
		"server_url (or LIGO_SERVER_URL): an https:// URL is required",
	} {
		if !strings.Contains(troubleshooting, msg) {
			t.Errorf("the troubleshooting table does not quote %q", msg)
		}
		if !strings.Contains(code, msg) {
			t.Errorf("%q is quoted by the guides and is not in the code", msg)
		}
	}
}

// TestEveryDiagramIsBuiltAndUsed: each diagram source holds one mermaid block
// and a guide includes its PDF.
func TestEveryDiagramIsBuiltAndUsed(t *testing.T) {
	text := guides(t)
	sources, err := filepath.Glob("common/diagrams/*.md")
	if err != nil || len(sources) == 0 {
		t.Fatalf("no diagrams: %v", err)
	}
	for _, s := range sources {
		if !strings.Contains(read(t, s), "```mermaid") {
			t.Errorf("%s has no mermaid block", s)
		}
		name := strings.TrimSuffix(filepath.Base(s), ".md")
		if !strings.Contains(text, "("+name+".pdf)") {
			t.Errorf("no guide includes %s.pdf", name)
		}
	}
}

// TestChaptersAreNumbered: each guide has metadata and numbered chapters
// (build.sh includes only NN-*.md, in order).
func TestChaptersAreNumbered(t *testing.T) {
	for _, g := range []string{"setup-guide", "operators-guide", "architecture"} {
		if _, err := os.Stat(filepath.Join(g, "metadata.yaml")); err != nil {
			t.Errorf("%s: %v", g, err)
		}
		chapters, _ := filepath.Glob(filepath.Join(g, "[0-9][0-9]-*.md"))
		if len(chapters) == 0 {
			t.Errorf("%s has no chapters", g)
		}
	}
}
