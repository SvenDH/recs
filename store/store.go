package recs

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
)

const (
	retainSnapshotCount = 2
	raftTimeout = 10 * time.Second
)

type Store struct {
	RaftDir  string
	RaftBind string
	inmem bool
	mu sync.Mutex
	m map[string]string // The key-value store for the system.
	raft *raft.Raft
	logger *log.Logger
}

type fsm Store

type command struct {
	Op string `json:"op,omitempty"`
	Key string `json:"key,omitempty"`
	Value string `json:"value,omitempty"`
}

func New(inmem bool) *Store {
	return &Store{
		m: make(map[string]string),
		inmem: inmem,
		logger: log.New(os.Stderr, "[store] ", log.LstdFlags),
	}
}

func (s *Store) Open(enableSingle bool, localID string) error {
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(localID)

	addr, err := net.ResolveTCPAddr("tcp", s.RaftBind)
	if err != nil {
		return err
	}
	transport, err := raft.NewTCPTransport(s.RaftBind, addr, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return err
	}
	snapshots, err := raft.NewFileSnapshotStore(s.RaftDir, retainSnapshotCount, os.Stderr)
	if err != nil {
		return fmt.Errorf("file snapshot store: %s", err)
	}

	var logStore raft.LogStore
	var stableStore raft.StableStore
	if s.inmem {
		logStore = raft.NewInmemStore()
		stableStore = raft.NewInmemStore()
	} else {
		boltDB, err := raftboltdb.New(raftboltdb.Options{
			Path: filepath.Join(s.RaftDir, "raft.db"),
		})
		if err != nil {
			return fmt.Errorf("new bbolt store: %s", err)
		}
		logStore = boltDB
		stableStore = boltDB
	}
	ra, err := raft.NewRaft(config, (*fsm)(s), logStore, stableStore, snapshots, transport)
	if err != nil {
		return fmt.Errorf("new raft: %s", err)
	}
	s.raft = ra
	if enableSingle {
		configuration := raft.Configuration{
			Servers: []raft.Server{{
				ID: config.LocalID,
				Address: transport.LocalAddr(),
			}},
		}
		ra.BootstrapCluster(configuration)
	}
	return nil
}

func (f *fsm) applySet(key, value string) interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.m[key] = value
	return nil
}

func (f *fsm) applyDelete(key string) interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	
	delete(f.m, key)
	return nil
}

func (f *fsm) Apply(l *raft.Log) interface{} {
	var c command
	if err := json.Unmarshal(l.Data, &c); err != nil {
		panic(fmt.Sprintf("failed to unmarshal command: %s", err.Error()))
	}
	switch c.Op {
	case "set":
		return f.applySet(c.Key, c.Value)
	case "delete":
		return f.applyDelete(c.Key)
	default:
		panic(fmt.Sprintf("unrecognized command op: %s", c.Op))
	}
}

func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o := make(map[string]string)
	for k, v := range f.m {
		o[k] = v
	}
	return &fsmSnapshot{store: o}, nil
}

func (f *fsm) Restore(rc io.ReadCloser) error {
	o := make(map[string]string)
	if err := json.NewDecoder(rc).Decode(&o); err != nil {
		return err
	}
	f.m = o
	return nil
}

type fsmSnapshot struct {
	store map[string]string
}

func (f *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	err := func() error {
		b, err := json.Marshal(f.store)
		if err != nil {
			return err
		}
		if _, err := sink.Write(b); err != nil {
			return err
		}
		return sink.Close()
	}()

	if err != nil {
		sink.Cancel()
	}
	return err
}

func (f *fsmSnapshot) Release() {}