package provider

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/dihedron/ligo/pkg/adminclient"
)

// allowedCalls are the methods of *adminclient.Client that the provider calls
// (spec §2, AC-P8). A chunk that needs one adds it here, in its own commit, so
// that the list is what a reviewer reads.
var allowedCalls = []string{
	"GetSource", "ListSources", "GetTarget", "ListTargets", "GetBinding", "ListBindings", "GetPlatform", "ListPlatforms",
	"SearchCatalog", "GetCatalogTarget", "GetCharter", "ListHolds", "CreatePlatform", "UpdatePlatform", "RetirePlatform", "CreateIdentityProvider", "UpdateIdentityProvider", "RetireIdentityProvider", "CreateSecretStore", "UpdateSecretStore", "RetireSecretStore", "CreateBinding", "UpdateBinding", "DeleteBinding", "CreateTarget", "UpdateTarget", "DeprecateTarget", "ReactivateTarget", "RetireTarget", "DeprecateAccessLevel", "ReactivateAccessLevel", "RetireAccessLevel", "CreateSource", "UpdateSource", "DeprecateSource", "ReactivateSource", "RetireSource", "WaitChangeRequest", "ListChangeRequests", "GetChangeRequest", "GetIdentityProvider", "ListIdentityProviders", "GetSecretStore", "ListSecretStores",
}

// decisionWords are what no method the provider calls may start with: the
// decisions of people (T-P3). Reading them (ListBreakGlass) is not deciding.
var decisionWords = []string{"approve", "decline", "ratify", "revert", "breakglass"}

// TestCalledMethods is AC-P8: the provider calls only the methods on its list,
// and none that decides. It reads the provider's sources for selectors whose
// name is a method of *adminclient.Client.
func TestCalledMethods(t *testing.T) {
	methods := map[string]bool{}
	typ := reflect.TypeFor[*adminclient.Client]()
	for i := range typ.NumMethod() {
		methods[typ.Method(i).Name] = true
	}
	// not calls of the domain: every type has them, and none reaches the server
	for _, common := range []string{"String", "GoString", "Close"} {
		delete(methods, common)
	}
	for _, w := range decisionWords {
		for m := range methods {
			if strings.HasPrefix(strings.ToLower(m), w) {
				t.Fatalf("the admin client has %s: the provider's guard needs rethinking", m)
			}
		}
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		tree, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(tree, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || !methods[sel.Sel.Name] {
				return true
			}
			if !slices.Contains(allowedCalls, sel.Sel.Name) {
				t.Errorf("%s: %s is not on the list of calls the provider may make", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}
