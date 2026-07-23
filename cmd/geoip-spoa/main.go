package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/andrewheberle/configger"
	"github.com/andrewheberle/geoip-spoa/internal/pkg/geoip"
	"github.com/andrewheberle/geoip-spoa/internal/pkg/spoa"
	"github.com/andrewheberle/slogger"
	"github.com/oklog/run"
	"github.com/spf13/pflag"
)

var Version = "dev"

func main() {
	lt := new(slogger.LoggerTypeVar)

	f := pflag.NewFlagSet("geoip-spoa", pflag.ContinueOnError)
	f.String("config", "", "Path to configuration file")
	f.String("listen", "127.0.0.1:3000", "SPOA listen address")
	f.String("locale", "en", "Locale for City names")
	f.String("db.asn", "/var/lib/GeoIP/GeoLite2-ASN.mmdb", "GeoLite2 ASN database path")
	f.String("db.city", "/var/lib/GeoIP/GeoLite2-City.mmdb", "GeoLite2 City database path")
	f.String("metrics.path", "/metrics", "Path for Prometheus metrics")
	f.String("metrics.listen", "", "Listen address for Prometheus metrics")
	f.Duration("interval", time.Hour*24, "Interval between checks for new GeoLite2 databases")
	f.Duration("cache.ttl", 0, "TTL for caching of IP lookups (0 to never expire)")
	f.Int("cache.size", 1024, "Number of IP lookups to cache (0 to disable)")
	f.Bool("debug", false, "Enable debug logging")
	f.Bool("version", false, "Show version and exit")
	f.Var(lt, "logger.type", "Logger type (auto, discard, json, systemd or text)")

	// parse command line
	if err := f.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing command line flags: %s\n", err)
		os.Exit(1)
	}

	// handle if version was requested
	if version, err := f.GetBool("version"); err == nil && version {
		fmt.Printf("%s %s\n", f.Name(), Version)
		os.Exit(0)
	}

	k, err := configger.LoadConfig(f, configger.WithEnvPrefix("geoip"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error loading configuration: %s\n", err)
		os.Exit(1)
	}

	// set up logger
	logLevel := new(slog.LevelVar)
	logger, err := slogger.NewLogger(logLevel, lt.LoggerTypeOption())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error setting up logger: %s\n", err)
		os.Exit(1)
	}
	if k.Bool("debug") {
		logLevel.Set(slog.LevelDebug)
	}

	// load databases
	var db geoip.DB
	asnPath := k.String("db.asn")
	cityPath := k.String("db.city")
	db, err = geoip.Open(asnPath, cityPath)
	if err != nil {
		logger.Error("there was an error loading the databases", "error", err, "asn", asnPath, "city", cityPath)
		os.Exit(1)
	}

	// set up cache
	cacheTtl := k.Duration("cache.ttl")
	cacheSize := k.Int("cache.size")
	if cacheSize > 0 {
		cache, err := geoip.NewCachingDB(db, cacheSize, cacheTtl)
		if err != nil {
			logger.Error("there was an error setting up the cache", "error", err, "ttl", cacheTtl, "size", cacheSize)
			os.Exit(1)
		}

		db = cache
	}

	listenString := k.String("listen")
	localeString := k.String("locale")
	srv, err := spoa.NewServer(listenString, db, spoa.WithLogger(logger), spoa.WithLocale(k.String("locale")))
	if err != nil {
		logger.Error("there was a problem setting up the server", "error", err, "listen", listenString, "locale", localeString)
		os.Exit(1)
	}

	g := run.Group{}

	g.Add(func() error {
		return srv.ListenAndServe()
	}, func(err error) {
		if err != nil {
			logger.Error("got error from server", "error", err, "listen", listenString, "locale", localeString)
		}
		_ = srv.Shutdown()
	})

	if metricsListen := k.String("metrics.listen"); metricsListen != "" {
		// set up metrics
		metricsPath := k.String("metrics.path")
		h := http.NewServeMux()
		h.Handle(metricsPath, srv.MetricsHandler())

		metrics := &http.Server{
			Addr:         metricsListen,
			Handler:      h,
			ReadTimeout:  time.Second * 2,
			WriteTimeout: time.Second * 2,
		}

		g.Add(func() error {
			logger.Info("starting metrics listener", "listen", metricsListen, "path", metricsPath)
			return metrics.ListenAndServe()
		}, func(err error) {
			if err != nil {
				logger.Error("got error from metrics listener", "error", err, "listen", metricsListen, "path", metricsPath)
			}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second*2)
				defer cancel()

				if err := metrics.Shutdown(ctx); err != nil {
					logger.Error("got error while shutting down metrics listener", "error", err, "listen", metricsListen, "path", metricsPath)
				}
			}()
		})
	}

	interval := k.Duration("interval")
	if interval != 0 {
		done := make(chan bool)

		g.Add(func() error {
			logger.Info("starting background reload of GeoLite2 databases", "interval", interval, "asn", asnPath, "city", cityPath)
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
