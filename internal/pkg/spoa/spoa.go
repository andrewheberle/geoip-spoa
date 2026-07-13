package spoa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/andrewheberle/geoip-spoa/internal/pkg/geoip"
	"github.com/negasus/haproxy-spoe-go/action"
	"github.com/negasus/haproxy-spoe-go/agent"
	"github.com/negasus/haproxy-spoe-go/request"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const MessageName = "geoip-lookup"

type Server struct {
	addr     string
	locale   string
	logger   *slog.Logger
	db       *geoip.DB
	registry *prometheus.Registry

	ctx    context.Context
	cancel context.CancelFunc

	// metrics
	requestErrors     prometheus.Counter
	requestTotal      prometheus.Counter
	requestDuration   *prometheus.HistogramVec
	reloadCheckErrors prometheus.Counter
	reloadCheckTotal  prometheus.Counter
	reloadTotal       prometheus.Counter
}

func NewServer(addr string, db *geoip.DB, opts ...ServerOption) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())

	s := &Server{
		addr:     addr,
		db:       db,
		logger:   slog.New(slog.DiscardHandler),
		locale:   "en",
		registry: prometheus.NewRegistry(),
		ctx:      ctx,
		cancel:   cancel,
	}

	for _, o := range opts {
		o(s)
	}

	// set up metrics
	s.requestErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "geoip_agent_request_error_total",
		Help: "Number of requests handled by the geoip lookup agent that had an error",
	})
	s.requestTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "geoip_agent_request_total",
		Help: "Total number of requests handled by the geoip lookup agent.",
	})
	s.requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "geoip_agent_request_duration_seconds",
			Help:    "Latency of request handling by the geoip lookup agent.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"status"},
	)
	s.reloadTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "geoip_agent_reload_total",
		Help: "Number of reloads of GeoIP databases.",
	})
	s.reloadCheckErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "geoip_agent_reload_check_error_total",
		Help: "Number of reload checks of the GeoIP databases that had an error.",
	})
	s.reloadCheckTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "geoip_agent_reload_check_total",
		Help: "Total number of reload checks of the GeoIP databases.",
	})

	// register metrics
	s.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		s.reloadCheckErrors,
		s.reloadCheckTotal,
		s.reloadTotal,
		s.requestDuration,
		s.requestErrors,
		s.requestTotal,
	)

	return s, nil
}

func (s *Server) ListenAndServe() error {
	s.logger.Info("starting SPOA server", "listen", s.addr, "locale", s.locale)
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		s.logger.Error("could not create listener", "error", err)
		return fmt.Errorf("could not create listener: %w", err)
	}

	a := agent.New(s.handler, s)

	errCh := make(chan error, 1)

	go func() {
		errCh <- a.Serve(l)
	}()

	select {
	case err := <-errCh:
		return err
	case <-s.ctx.Done():
		closeErr := l.Close()
		serveErr := <-errCh
		if closeErr != nil {
			return closeErr
		}
		if errors.Is(serveErr, net.ErrClosed) {
			return s.ctx.Err()
		}
		return serveErr
	}
}

func (s *Server) Shutdown() error {
	s.cancel()
	return nil
}

func (s *Server) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(s.registry, promhttp.HandlerOpts{
		Registry: s.registry,
	})
}

func (s *Server) handler(req *request.Request) {
	start := time.Now()
	status := "success"
	defer func() {
		s.requestDuration.WithLabelValues(status).Observe(time.Since(start).Seconds())
	}()
	s.requestTotal.Inc()

	logger := s.logger.With("engineID", req.EngineID, "streamID", req.StreamID, "frameID", req.FrameID, "messages", req.Messages.Len())

	msg, err := req.Messages.GetByName(MessageName)
	if err != nil {
		status = "error"
		s.requestErrors.Inc()
		s.logger.Info("message was not found")
		return
	}

	ipValue, ok := msg.KV.Get("ip")
	if !ok {
		status = "error"
		s.requestErrors.Inc()
		logger.Warn("ip was not found in message")
		return
	}

	ip, ok := ipValue.(net.IP)
	if !ok {
		status = "error"
		s.requestErrors.Inc()
		logger.Warn("ip has incorrect type expected IP address")
		return
	}
	logger = logger.With("ip", ip)

	asn, city, err := s.db.Lookup(ip)
	if err != nil {
		status = "error"
		s.requestErrors.Inc()
		logger.Error("error looking up ip address", "error", err)
		return
	}

	// add info to request
	req.Actions.SetVar(action.ScopeTransaction, "asn", asn.AutonomousSystemNumber)
	req.Actions.SetVar(action.ScopeTransaction, "continent", city.Continent.Code)
	req.Actions.SetVar(action.ScopeTransaction, "country", city.Country.ISOCode)
	if city, ok := city.City.Names[s.locale]; ok {
		req.Actions.SetVar(action.ScopeTransaction, "city", city)
		logger = logger.With("city", city)
	}

	logger.Debug("handled request", "asn", asn.AutonomousSystemNumber, "continent", city.Continent.Code, "country", city.Country.ISOCode)
}

func (s *Server) Errorf(format string, args ...any) {
	s.logger.Error(fmt.Sprintf(format, args...))
}
