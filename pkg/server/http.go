// Copyright 2025 The Sigstore Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	pb "github.com/sigstore/rekor-tiles/pkg/generated/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type httpProxy struct {
	*http.Server
	serverEndpoint string
}

func newHTTPProxy(ctx context.Context, config *HTTPConfig, grpcServer *grpcServer) *httpProxy {
	mux := runtime.NewServeMux()

	// TODO: allow TLS if the startup provides a TLS cert, but for now the proxy connects to grpc without TLS
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	err := pb.RegisterRekorHandlerFromEndpoint(ctx, mux, grpcServer.serverEndpoint, opts)
	if err != nil {
		slog.Error("Failed to register gateway:", "errors", err)
		os.Exit(1)
	}

	// TODO: configure https connection preferences (time-out, max size, etc)

	endpoint := fmt.Sprintf("%s:%v", config.host, config.port)
	return &httpProxy{
		Server: &http.Server{
			Addr:    endpoint,
			Handler: mux,

			ReadTimeout:       60 * time.Second,
			ReadHeaderTimeout: 60 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       config.idleTimeout,
		},
		serverEndpoint: endpoint,
	}
}

func (hp *httpProxy) start(wg *sync.WaitGroup) {

	slog.Info("starting http proxy", "address", hp.serverEndpoint)

	waitToClose := make(chan struct{})
	go func() {
		// capture interrupts and shutdown Server
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, syscall.SIGINT, syscall.SIGTERM)
		<-sigint

		if err := hp.Shutdown(context.Background()); err != nil {
			slog.Info("http Server Shutdown error", "errors", err)
		}
		close(waitToClose)
		slog.Info("stopped http Server")
	}()

	wg.Add(1)
	go func() {
		if err := hp.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("could not start http listener", "errors", err)
			os.Exit(1)
		}
		<-waitToClose
		wg.Done()
		slog.Info("http Server shutdown")
	}()
}
