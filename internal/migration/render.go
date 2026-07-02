package migration

import (
	"strings"

	"gopkg.in/yaml.v3"
)

// RenderMatrix renders the migration matrix as a GitHub-flavoured markdown table (deterministic).
func RenderMatrix(rows []MatrixRow) string {
	var b strings.Builder
	b.WriteString("# V1 → V2 Migration Matrix\n\n")
	b.WriteString("| Category | Found | Imported | Skipped | Missing | Validation | Notes |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, r := range rows {
		b.WriteString("| " + cell(r.Category) + " | " + cell(r.Found) + " | " + cell(r.Imported) + " | " +
			cell(r.Skipped) + " | " + cell(r.Missing) + " | " + cell(r.Validation) + " | " + cell(r.Notes) + " |\n")
	}
	return b.String()
}

func cell(s string) string {
	if s == "" {
		return "—"
	}
	return strings.ReplaceAll(s, "|", "\\|")
}

// renderYAML marshals the migrated config fragment (a header comment + yaml).
func renderYAML(c MigratedConfig) string {
	b, _ := yaml.Marshal(c)
	return "# ClaudWorker V2 — config fragment migrated from V1 (merge into cwv2.yaml).\n" +
		"# Generated read-only from V1; contains NO secret values.\n" + string(b)
}
