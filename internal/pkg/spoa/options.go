package spoa

import "log/slog"

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
