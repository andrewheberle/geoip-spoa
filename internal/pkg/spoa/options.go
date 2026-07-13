package spoa

import (
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

type ServerOption func(*Server)

func WithLogger(logger *slog.Logger) ServerOption {
	return func(s *Server) {
		s.logger = logger
	}
}

func WithLocale(locale string) ServerOption {
	return func(s *Server) {
		s.locale = locale
	}
}

func WithRegistry(registry *prometheus.Registry) ServerOption {
	return func(s *Server) {
		s.registry = registry
	}
}
