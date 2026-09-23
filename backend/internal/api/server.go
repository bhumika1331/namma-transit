package api

import (
	"net/http"

	connectcors "connectrpc.com/cors"
	"github.com/rs/cors"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/bhumika1331/namma-transit/backend/gen/transit/v1/transitv1connect"
)

// Options wires the service implementations into one HTTP handler.
type Options struct {
	Meta           transitv1connect.MetaServiceHandler
	Place          transitv1connect.PlaceServiceHandler // may be nil until implemented
	Trip           transitv1connect.TripServiceHandler  // may be nil until implemented
	AllowedOrigins []string
}

// NewHandler returns a handler that speaks Connect, gRPC and gRPC-Web on the
// same port over HTTP/1.1 or HTTP/2 (h2c), with CORS for the web frontend.
func NewHandler(opts Options) http.Handler {
	mux := http.NewServeMux()
	if opts.Meta != nil {
		mux.Handle(transitv1connect.NewMetaServiceHandler(opts.Meta))
	}
	if opts.Place != nil {
		mux.Handle(transitv1connect.NewPlaceServiceHandler(opts.Place))
	}
	if opts.Trip != nil {
		mux.Handle(transitv1connect.NewTripServiceHandler(opts.Trip))
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	origins := opts.AllowedOrigins
	if len(origins) == 0 {
		origins = []string{"http://localhost:3000"}
	}
	c := cors.New(cors.Options{
		AllowedOrigins: origins,
		AllowedMethods: connectcors.AllowedMethods(),
		AllowedHeaders: connectcors.AllowedHeaders(),
		ExposedHeaders: connectcors.ExposedHeaders(),
		MaxAge:         7200,
	})
	return h2c.NewHandler(c.Handler(mux), &http2.Server{})
}
