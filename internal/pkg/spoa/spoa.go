package spoa

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/andrewheberle/geoip-spoa/internal/pkg/geoip"
	"github.com/negasus/haproxy-spoe-go/action"
	"github.com/negasus/haproxy-spoe-go/agent"
	"github.com/negasus/haproxy-spoe-go/request"
)

const MessageName = "geoip-lookup"

type Server struct {
	addr   string
	locale string
	logger *slog.Logger
	db     *geoip.DB
	ctx    context.Context
	cancel context.CancelFunc
}

func NewServer(addr string, db *geoip.DB, opts ...ServerOption) (*Server, error) {
	ctx, cancel := context.WithCancel(context.Background())

	s := &Server{
		addr:   addr,
		db:     db,
		logger: slog.New(slog.DiscardHandler),
		locale: "en",
		ctx:    ctx,
		cancel: cancel,
	}

	for _, o := range opts {
		o(s)
	}

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

func (s *Server) handler(req *request.Request) {
	s.logger.Debug("handling request", "engineID", req.EngineID, "streamID", req.StreamID, "frameID", req.FrameID, "messages", req.Messages.Len())

	msg, err := req.Messages.GetByName(MessageName)
	if err != nil {
		s.logger.Debug("message was not found")
		return
	}

	ipValue, ok := msg.KV.Get("ip")
	if !ok {
		s.logger.Error("ip was not found in message")
		return
	}

	ip, ok := ipValue.(net.IP)
	if !ok {
		s.logger.Error("ip has incorrect type expected IP address")
		return
	}

	asn, city, err := s.db.Lookup(ip)
	if err != nil {
		s.logger.Warn("error looking up ip address", "error", err)
	}

	// add info to request
	req.Actions.SetVar(action.ScopeTransaction, "asn", asn.AutonomousSystemNumber)
	req.Actions.SetVar(action.ScopeTransaction, "continent", city.Continent.Code)
	req.Actions.SetVar(action.ScopeTransaction, "country", city.Country.ISOCode)
	if city, ok := city.City.Names[s.locale]; ok {
		req.Actions.SetVar(action.ScopeTransaction, "city", city)
	}
}

func (s *Server) Errorf(format string, args ...any) {
	s.logger.Error(fmt.Sprintf(format, args...))
}
