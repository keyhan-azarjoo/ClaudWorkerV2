package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/myotgo/ClaudWorkerV2/internal/backup"
	"github.com/myotgo/ClaudWorkerV2/internal/config"
	"github.com/myotgo/ClaudWorkerV2/internal/doctor"
	"github.com/myotgo/ClaudWorkerV2/internal/enginehome"
	"github.com/myotgo/ClaudWorkerV2/internal/logging"
)

// cmdValidate is the startup/config validator (production gate). It loads the config and runs the full
// doctor check; exit 0 only if nothing fails.
func cmdValidate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cwv2.yaml (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *cfgPath == "" {
		fmt.Fprintln(os.Stderr, "cwv2 validate: --config is required")
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return emitErr(fmt.Errorf("config invalid: %w", err))
	}
	log := logging.Default()
	rep := doctor.Run(cfg, doctor.Options{})
	for _, c := range rep.Checks {
		switch c.Status {
		case doctor.Fail:
			log.Error("check", "name", c.Name, "detail", c.Detail)
		case doctor.Warn:
			log.Warn("check", "name", c.Name, "detail", c.Detail)
		}
	}
	ok, warn, fail := rep.Counts()
	if !rep.OK() {
		log.Error("validate: FAIL", "ok", ok, "warn", warn, "fail", fail)
		return 1
	}
	log.Info("validate: OK", "ok", ok, "warn", warn, "fail", fail)
	return 0
}

// cmdBackup backs up the project's DURABLE engine-home state to a tar.gz (transient state excluded).
func cmdBackup(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cwv2.yaml (required)")
	to := fs.String("to", "", "output archive path, e.g. backup.tgz (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *cfgPath == "" || *to == "" {
		fmt.Fprintln(os.Stderr, "cwv2 backup: --config and --to are required")
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return emitErr(err)
	}
	l := enginehome.For(cfg.EngineHome, cfg.Project)
	if err := backup.Backup(l.ProjectDir, *to, nil); err != nil {
		return emitErr(err)
	}
	emit(map[string]any{"backed_up": l.ProjectDir, "archive": *to, "excluded": backup.DefaultExcludes})
	return 0
}

// cmdRestore restores a backup archive into the project's engine-home dir.
func cmdRestore(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to cwv2.yaml (required)")
	from := fs.String("from", "", "backup archive path (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *cfgPath == "" || *from == "" {
		fmt.Fprintln(os.Stderr, "cwv2 restore: --config and --from are required")
		return 2
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return emitErr(err)
	}
	l := enginehome.For(cfg.EngineHome, cfg.Project)
	if err := backup.Restore(*from, l.ProjectDir); err != nil {
		return emitErr(err)
	}
	emit(map[string]any{"restored_to": l.ProjectDir, "archive": *from})
	return 0
}
