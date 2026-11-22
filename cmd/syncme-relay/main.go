// syncme-relay forwards encrypted Syncme connections between a phone and a
// PC that cannot reach each other directly. Run it on any machine with a
// public IP (a small VPS is plenty) and set "relay" in the PC config.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	"github.com/CosmicKittu/Syncme/internal/proto"
	"github.com/CosmicKittu/Syncme/internal/relay"
)

func main() {
	addr := flag.String("listen", fmt.Sprintf(":%d", proto.RelayPort), "address to listen on")
	max := flag.Int64("max-conns", 2000, "maximum simultaneous connections")
	flag.Parse()
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	logger := log.New(os.Stderr, "", log.LstdFlags)
	logger.Printf("syncme-relay listening on %s", ln.Addr())
	s := &relay.Server{Log: logger, MaxConns: *max}
	log.Fatal(s.Serve(ln))
}
