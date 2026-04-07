package main

import (
	"log"

	"github.com/afkarxyz/SpotiFLAC/api"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	cfg := api.DefaultConfig()
	srv := api.NewServer(cfg)

	if err := srv.Run(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
