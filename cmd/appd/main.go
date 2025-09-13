package main

import (
	"flag"
	"log"

	abciserver "github.com/cometbft/cometbft/abci/server"
	abcitypes "github.com/cometbft/cometbft/abci/types"

	"github.com/dik654/comet-bft-optimistic-parallel-execution-poc/internal/app"
)

func main() {
	listen := flag.String("listen", "tcp://127.0.0.1:26658", "ABCI listen address")
	flag.Parse()

	application := app.NewApp()

	srv, err := abciserver.NewServer(*listen, "socket", application)
	if err != nil {
		log.Fatalf("abci server: %v", err)
	}

	err = srv.Start()
	if err != nil {
		log.Fatalf("abci start: %v", err)
	}
	defer func() {
		if err := srv.Stop(); err != nil {
			log.Printf("abci stop: %v", err)
		}
	}()

	serviceTrap := make(chan struct{})
	<-serviceTrap
	_ = abcitypes.Response_Info{}
}
