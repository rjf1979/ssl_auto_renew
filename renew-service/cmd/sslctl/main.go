package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/example/ssl-auto-renew/internal/config"
	"github.com/example/ssl-auto-renew/internal/service"
	"github.com/example/ssl-auto-renew/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(os.Args[1:]); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: sslctl config validate|status|renew [flags]")
	}
	switch args[0] {
	case "config":
		if len(args) >= 2 && args[1] == "validate" {
			return validateConfig(args[2:])
		}
		return errors.New("usage: sslctl config validate -config /etc/ssl-auto-renew/config.yaml")
	case "status":
		return status(args[1:])
	case "renew":
		return renew(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func validateConfig(args []string) error {
	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	path := fs.String("config", "/etc/ssl-auto-renew/config.yaml", "configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	fmt.Printf("configuration is valid: %s\n", *path)
	return nil
}

func status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	path := fs.String("config", "/etc/ssl-auto-renew/config.yaml", "configuration file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	state, err := store.Load(filepath.Join(cfg.Storage.DataDir, "state.json"))
	if err != nil {
		return err
	}
	for _, item := range cfg.Certificates {
		remaining, err := service.RemainingDays(item.Deploy.FullchainFile)
		if err != nil {
			fmt.Printf("%-24s unavailable (%v)\n", item.Name, err)
			continue
		}
		last := state.Certificates[item.Name]
		fmt.Printf("%-24s %d days remaining, last=%s\n", item.Name, remaining, last.LastResult)
	}
	return nil
}

func renew(args []string) error {
	fs := flag.NewFlagSet("renew", flag.ContinueOnError)
	path := fs.String("config", "/etc/ssl-auto-renew/config.yaml", "configuration file")
	force := fs.Bool("force", false, "renew even when outside the renewal window")
	name := fs.String("name", "", "renew only the named certificate")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	return service.New(cfg).Renew(context.Background(), service.RenewOptions{Force: *force, Name: *name, Now: time.Now()})
}
