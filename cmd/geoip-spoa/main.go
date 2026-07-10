package main

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/andrewheberle/geoip-spoa/internal/pkg/geoip"
	"github.com/andrewheberle/geoip-spoa/internal/pkg/spoa"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/oklog/run"
	"github.com/spf13/pflag"
)

var Version = "dev"

func main() {
	f := pflag.NewFlagSet("config", pflag.ContinueOnError)
	f.Usage = func() {
		fmt.Println(f.FlagUsages())
		os.Exit(0)
	}
	f.String("config", "", "Path to configuration file")
	f.String("listen", "127.0.0.1:3000", "SPOA listen address")
	f.String("locale", "en", "Locale for names")
	f.String("geoip.asn", "", "GeoLite2 ASN database path")
	f.String("geoip.city", "", "GeoLite2 City database path")
	f.Duration("interval", time.Hour*1, "Interval between checks for new GeoLite2 databases")
	f.Bool("debug", false, "Enable debug logging")
	f.Bool("version", false, "Show version and exit")

	// parse command line
	if err := f.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing command line flags: %s\n", err)
		os.Exit(1)
	}

	// handle if version was requested
	if version, err := f.GetBool("version"); err == nil && version {
		fmt.Printf("geoip-spoa %s\n", Version)
		os.Exit(0)
	}

	k := koanf.New(".")

	// load any config file
	if config, err := f.GetString("config"); err != nil {
		fmt.Fprintf(os.Stderr, "error getting flag value: %s\n", err)
		os.Exit(1)
	} else if config != "" {
		if err := k.Load(file.Provider(config), yaml.Parser()); err != nil {
			fmt.Fprintf(os.Stderr, "error loading configuration: %s\n", err)
			os.Exit(1)
		}
	}

	// Load env vars
	if err := k.Load(env.Provider(".", env.Opt{
		Prefix: "GEOIP_",
		TransformFunc: func(k, v string) (string, any) {
			// Transform the key.
			k = strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(k, "GEOIP_")), "_", ".")

			// Transform values with commas into slices
			if strings.Contains(v, ",") {
				return k, strings.Split(v, ",")
			}

			return k, v
		},
	}), nil); err != nil {
		fmt.Fprintf(os.Stderr, "error reading env vars: %s\n", err)
		os.Exit(1)
	}

	// Load command line options
	if err := k.Load(posflag.Provider(f, ".", k), nil); err != nil {
		fmt.Fprintf(os.Stderr, "error reading command line: %s\n", err)
		os.Exit(1)
	}

	// set up logger
	logLevel := new(slog.LevelVar)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	if k.Bool("debug") {
		logLevel.Set(slog.LevelDebug)
	}

	// load databases
	db, err := geoip.Open(k.String("geoip.asn"), k.String("geoip.city"))
	if err != nil {
		logger.Error("there was an error loading the databases", "error", err)
		os.Exit(1)
	}

	srv, err := spoa.NewServer(k.String("listen"), db, spoa.WithLogger(logger), spoa.WithLocale(k.String("locale")))
	done := make(chan bool)

	g := run.Group{}
	g.Add(func() error {
		return srv.ListenAndServe()
	}, func(err error) {
		if err != nil {
			logger.Error("got error from server", "error", err)
		}
		_ = srv.Shutdown()
	})
	interval := k.Duration("interval")
	if interval != 0 {
		g.Add(func() error {
			logger.Info("starting background reload of GeoLite2 databases", "interval", interval)
			t := time.NewTicker(interval)
			defer t.Stop()

			for {
				select {
				case <-done:
					return nil
				case <-t.C:
					if changed, err := db.Reload(); err != nil {
						logger.Warn("there was an error while reloading databases", "error", err)
					} else {
						if changed {
							logger.Info("new database version loaded")
						} else {
							logger.Info("no changes detected to databases")
						}
					}
				}
			}
		}, func(err error) {
			done <- true
		})
	}

	if err := g.Run(); err != nil {
		logger.Error("got an error", "error", err)
		os.Exit(1)
	}
}
