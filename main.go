package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"

	"github.com/SvenDH/recs/cluster"
	"github.com/SvenDH/recs/modules"
	"github.com/SvenDH/recs/modules/physics"
)

var (
	inmem    = flag.Bool("inmem", false, "Use in-memory storage for Raft")
	wal      = flag.Bool("wal", true, "Use on-disk write-ahead log for Raft")
	raftAddr = flag.String("raddr", "127.0.0.1:12000", "TCP host+port for the raft chatter for this node")
	httpAddr = flag.String("haddr", "127.0.0.1:8080", "HTTP host+port for this node")
	joinAddr = flag.String("join", "", "Host+port of leader to join")
	nodeID   = flag.String("id", "node0", "Node id used by Raft")
)

func main() {
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintf(os.Stderr, "No Raft storage directory specified\n")
		os.Exit(1)
	}
	if *nodeID == "" {
		nodeID = raftAddr
	}
	raftDir := flag.Arg(0)
	if raftDir == "" {
		log.Fatalln("No Raft storage directory specified")
	}

	s := cluster.NewStore(raftDir, *inmem, *wal)
	
	s.Bind = *raftAddr
	if err := s.Open(*joinAddr == "", *nodeID); err != nil {
		log.Fatalf("failed to open store: %s", err.Error())
	}

	h := NewServer(*httpAddr, s)
	if err := h.Start(); err != nil {
		log.Fatalf("failed to start HTTP service: %s", err.Error())
	}

	if *joinAddr != "" {
		if err := join(*joinAddr, *raftAddr, *nodeID); err != nil {
			log.Fatalf("failed to join node at %s: %s", *joinAddr, err.Error())
		}
	}

	log.Printf("hraftd started successfully, listening on http://%s", *httpAddr)

	modules.RegisterBase(s)
	modules.RegisterChat(s)
	modules.RegisterSprite(s)
	modules.RegisterTiles(s)
	physics.RegisterPhysics(s)

	terminate := make(chan os.Signal, 1)
	signal.Notify(terminate, os.Interrupt)
	<-terminate
	log.Println("hraftd exiting")
}

func join(joinAddr, raftAddr, nodeID string) error {
	b, err := json.Marshal(map[string]string{"addr": raftAddr, "id": nodeID})
	if err != nil {
		return err
	}
	resp, err := http.Post(fmt.Sprintf("http://%s/join", joinAddr), "application-type/json", bytes.NewReader(b))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}
