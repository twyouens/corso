// Command seed fills the Microsoft 365 test tenant with known,
// recognisably-prefixed data across the v1 workloads (Exchange mail,
// calendar, contacts; OneDrive; SharePoint document library and list), and
// writes a seed manifest describing exactly what it put there. Running it
// twice leaves the tenant unchanged: see src/internal/seed for how.
//
// Run it through the workspace's test-tenant credential loader, from
// src/ (there is no go.mod at the corso repository root, only here -
// `go run ./src/cmd/seed` from the repository root does not work):
//
//	cd src && GO111MODULE=on GOWORK=off <workspace>/scripts/with-test-tenant.sh go run ./cmd/seed
//
// This is the one place in this program that reads the process
// environment; src/internal/seed takes every value as an explicit
// argument. See docs/test-tenant-harness.md for the full list of
// environment variables this command requires.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/twyouens/corso/src/internal/common/crash"
	"github.com/twyouens/corso/src/internal/seed"
	"github.com/twyouens/corso/src/pkg/account"
	"github.com/twyouens/corso/src/pkg/control"
	"github.com/twyouens/corso/src/pkg/count"
	"github.com/twyouens/corso/src/pkg/credentials"
	"github.com/twyouens/corso/src/pkg/logger"
	"github.com/twyouens/corso/src/pkg/services/m365/api"
	"github.com/twyouens/corso/src/pkg/services/m365/api/graph"
)

func main() {
	// Credential validation (Done-when #7): check every required env var is
	// present before doing anything else, and if any are missing, report
	// their names - never a value - and stop.
	missing := seed.MissingVars(os.Getenv, seed.RequiredEnvVars)
	if len(missing) > 0 {
		fmt.Fprintln(os.Stderr, seed.MissingVarsError(missing))
		os.Exit(2)
	}

	cfg := configFromEnv()

	ls := logger.Settings{
		Level:  logger.LLInfo,
		Format: logger.LFText,
	}

	ctx, _ := logger.CtxOrSeed(context.Background(), ls)

	defer func() {
		if err := crash.Recovery(ctx, recover(), "seed"); err != nil {
			logger.CtxErr(ctx, err).Error("panic in seed")
		}

		logger.Flush(ctx)
	}()

	graph.InitializeConcurrencyLimiter(ctx, true, 4)

	creds := account.M365Config{
		M365: credentials.M365{
			AzureClientID:     cfg.ClientID,
			AzureClientSecret: cfg.ClientSecret,
		},
		AzureTenantID: cfg.TenantID,
	}

	client, err := api.NewClient(creds, control.DefaultOptions(), count.New())
	if err != nil {
		logger.CtxErr(ctx, err).Error("building Graph client")
		logger.Flush(ctx)
		os.Exit(1)
	}

	manifest, err := seed.Run(ctx, client, cfg, func(line string) {
		fmt.Println(line)
	})
	if err != nil {
		logger.CtxErr(ctx, err).Error("seeding test tenant")
		logger.Flush(ctx)
		os.Exit(1)
	}

	fmt.Printf(
		"seeded %d item(s) across %d workload(s); manifest written to %s\n",
		len(manifest.Items),
		countWorkloads(manifest),
		cfg.ManifestPath)
}

// configFromEnv is the one function in this program (besides main itself)
// that reads the process environment; everything downstream takes a
// seed.Config built here as an explicit argument.
func configFromEnv() seed.Config {
	manifestPath := os.Getenv(seed.EnvManifestPath)
	if manifestPath == "" {
		manifestPath = seed.DefaultManifestPath
	}

	return seed.Config{
		TenantID:     os.Getenv(seed.EnvTenantID),
		ClientID:     os.Getenv(seed.EnvClientID),
		ClientSecret: os.Getenv(seed.EnvClientSecret),
		UserID:       os.Getenv(seed.EnvSeedUserID),
		SiteID:       os.Getenv(seed.EnvSeedSiteID),
		ManifestPath: manifestPath,
	}
}

func countWorkloads(m seed.Manifest) int {
	seen := map[seed.Workload]struct{}{}
	for _, item := range m.Items {
		seen[item.Workload] = struct{}{}
	}

	return len(seen)
}
