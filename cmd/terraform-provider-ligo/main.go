// Command terraform-provider-ligo is the Terraform and OpenTofu provider of
// ligo. Terraform and OpenTofu start it and talk to it over gRPC.
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/dihedron/terraform-provider-ligo/internal/provider"
	"github.com/dihedron/terraform-provider-ligo/pkg/metadata"
)

func main() {
	var debug, version, full bool
	flag.BoolVar(&debug, "debug", false, "serve for a debugger, which attaches through TF_REATTACH_PROVIDERS")
	flag.BoolVar(&version, "version", false, "print the version and exit")
	flag.BoolVar(&full, "version-full", false, "print the full build information and exit")
	flag.Parse()

	if full {
		metadata.PrintFull(os.Stdout)
		return
	}
	if version {
		metadata.Print(os.Stdout)
		return
	}

	err := providerserver.Serve(context.Background(), provider.New(metadata.Version), providerserver.ServeOpts{
		Address: "registry.terraform.io/dihedron/ligo",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
