package sample

import "fmt"

// Server handles HTTP connections.
type Server struct {
	Port int
	Host string
}

// Config holds application configuration.
type Config struct {
	Debug bool
}

// NewServer creates a new Server instance with default settings.
func NewServer(port int) *Server {
	return &Server{Port: port, Host: "localhost"}
}

// Start starts the HTTP server on the configured port.
func (s *Server) Start() error {
	fmt.Printf("Starting server on %s:%d\n", s.Host, s.Port)
	return nil
}

// unexportedFunc is not captured.
func unexportedFunc() {}

// TODO: This is a structured todo comment that should be captured as memory.

// FIXME: There is a known issue with the connection pool under high load scenarios.
