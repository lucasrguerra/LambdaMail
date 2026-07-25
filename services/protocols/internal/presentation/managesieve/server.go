package managesievepresentation

import (
	"net"

	"lambdamail/protocols/internal/application/port"
	appusecase "lambdamail/protocols/internal/application/usecase"
)

type Server struct {
	addr        string
	useCase     *appusecase.ManageSieveSessionUseCase
	tlsProvider port.CertProvider
	listener    net.Listener
}

func NewServer(addr string, useCase *appusecase.ManageSieveSessionUseCase, tlsProvider port.CertProvider) *Server {
	return &Server{
		addr:        addr,
		useCase:     useCase,
		tlsProvider: tlsProvider,
	}
}

func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = ln
	return s.Serve(ln)
}

func (s *Server) Serve(ln net.Listener) error {
	s.listener = ln
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go func(rawConn net.Conn) {
			connHandler := newConn(rawConn, s.useCase, s.tlsProvider)
			connHandler.serve()
		}(c)
	}
}

func (s *Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}
