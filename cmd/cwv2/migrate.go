package main

import (
	"flag"
	"fmt"
	"os"

	"claudworker/internal/migration"
)

func migrateUsage() {
	fmt.Fprint(os.Stderr, `cwv2 migrate — import ClaudWorker V1 config into V2 artifacts (read-only against V1)

  cwv2 migrate --from <V1 dir> --to <output dir> [--dry-run]

Reads V1 persistent config (accounts, devices, usage guard, scheduling, gate labels), maps it to V2
resources + a config fragment, and writes a migration matrix. Never writes to V1, never emits secret
values, idempotent (safe to re-run). --dry-run prints the matrix and writes nothing.
`)
}

func cmdMigrate(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	from := fs.String("from", "", "ClaudWorker V1 directory (required)")
	to := fs.String("to", "", "output directory for V2 artifacts (required unless --dry-run)")
	dryRun := fs.Bool("dry-run", false, "print the migration matrix; write nothing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *from == "" {
		fmt.Fprintln(os.Stderr, "cwv2 migrate: --from is required")
		migrateUsage()
		return 2
	}
	if _, err := os.Stat(*from); err != nil {
		return emitErr(fmt.Errorf("V1 dir not found: %s", *from))
	}

	data, err := migration.Read(*from) // read-only
	if err != nil {
		return emitErr(err)
	}
	res := migration.Migrate(data)

	if *dryRun {
		fmt.Print(migration.RenderMatrix(res.Matrix))
		return 0
	}
	if *to == "" {
		fmt.Fprintln(os.Stderr, "cwv2 migrate: --to is required (or use --dry-run)")
		return 2
	}
	if err := migration.Write(res, *to); err != nil {
		return emitErr(err)
	}
	accounts, devices := 0, 0
	for _, r := range res.Resources {
		switch r.Kind {
		case "claude_account", "codex_account":
			accounts++
		default:
			devices++
		}
	}
	emit(map[string]any{
		"from": *from, "to": *to,
		"resources": len(res.Resources), "accounts": accounts, "devices": devices,
		"matrix_rows": len(res.Matrix),
		"artifacts":   []string{"resources.json", "migrated.yaml", "migration-matrix.json", "migration-matrix.md"},
	})
	return 0
}
