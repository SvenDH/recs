package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	transport "github.com/Jille/raft-grpc-transport"
	"github.com/SvenDH/recs/events"
	pb "github.com/SvenDH/recs/proto"
	world "github.com/SvenDH/recs/world"
	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/encoding/gzip"
)

const (
	retainSnapshotCount = 2
	raftTimeout         = 10 * time.Second
)

type conn struct {
	clientConn *grpc.ClientConn
	client     pb.RecsClient
	mtx        sync.Mutex
}

type Server struct {
	events.Broker
	Bind        string
	Dir         string
	DialOptions []grpc.DialOption
	RpcTimeout  time.Duration
	inmem       bool
	wal         bool
	components  []reflect.Type
	systems     []world.System

	mu  sync.Mutex
	s2w map[string][]string // The worlds per node id
	w2s map[string]string   // The world name to node id map

	connectionsMtx sync.Mutex
	connections    map[raft.ServerID]*conn

	wm *world.WorldManager

	raft   *raft.Raft
	logger *log.Logger
}
type fsm Server

type command struct {
	Op    string `json:"o,omitempty"`
	Key   string `json:"k,omitempty"`
	Value string `json:"v,omitempty"`
}

type fsmSnapshot struct {
	store map[string]string
}

func NewServer(inmem bool, wal bool) *Server {
	return &Server{
		Broker:      events.NewMemoryBroker(),
		DialOptions: []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
		RpcTimeout:  10 * time.Second,
		s2w:         make(map[string][]string),
		w2s:         make(map[string]string),
		inmem:       inmem,
		wal:         wal,
		components:  make([]reflect.Type, 0),
		systems:     make([]world.System, 0),
		connections: make(map[raft.ServerID]*conn),
		logger:      log.New(os.Stderr, "[store]: ", log.LstdFlags),
	}
}

func RegisterComponent[T any](s *Server) {
	tp := reflect.TypeOf((*T)(nil)).Elem()
	s.components = append(s.components, tp)
}

func RegisterSystem(s *Server, systems ...world.System) {
	s.systems = append(s.systems, systems...)
}

func (s *Server) Open(enableSingle bool, localID string) error {
	wm := world.NewWorldManager(s.inmem, filepath.Join(s.Dir, "ecs"), s.wal, s)
	for _, c := range s.components {
		wm.Components[strings.ToLower(c.Name())] = c
	}
	wm.Systems = append(wm.Systems, s.systems...)
	s.wm = wm

	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(localID)
	
	dir := filepath.Join(s.Dir, localID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		log.Fatalf("failed to create path for Raft storage: %s", err.Error())
	}
	sn, err := raft.NewFileSnapshotStore(dir, retainSnapshotCount, os.Stderr)
	if err != nil {
		return fmt.Errorf("file snapshot store: %s", err)
	}
	var ls raft.LogStore
	var ss raft.StableStore
	if s.inmem {
		ls = raft.NewInmemStore()
		ss = raft.NewInmemStore()
	} else {
		boltDB, err := raftboltdb.New(raftboltdb.Options{
			Path: filepath.Join(dir, "raft.db"),
		})
		if err != nil {
			return fmt.Errorf("new bbolt store: %s", err)
		}
		ls = boltDB
		ss = boltDB
	}

	tm := transport.New(raft.ServerAddress(s.Bind), s.DialOptions)
	ra, err := raft.NewRaft(config, (*fsm)(s), ls, ss, sn, tm.Transport())
	if err != nil {
		return fmt.Errorf("new raft: %s", err)
	}
	s.raft = ra
	if enableSingle {
		configuration := raft.Configuration{
			Servers: []raft.Server{{
				ID:      config.LocalID,
				Address: raft.ServerAddress(s.Bind),
			}},
		}
		ra.BootstrapCluster(configuration)
	}

	server := grpc.NewServer()
	encoding.RegisterCompressor(encoding.GetCompressor(gzip.Name))
	tm.Register(server)
	Register(server, s.Broker, s.wm)

	_, port, err := net.SplitHostPort(s.Bind)
	if err != nil {
		log.Fatalf("failed to parse local address (%q): %v", s.Bind, err)
	}
	sock, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	go server.Serve(sock)

	return nil
}

func (s *Server) Close() {
	for _, c := range s.connections {
		c.clientConn.Close()
	}
	s.Broker.Close()
}

func (s *Server) getPeer(id raft.ServerID, target raft.ServerAddress) (pb.RecsClient, error) {
	// Connection pool for rpc and publishing
	s.connectionsMtx.Lock()
	c, ok := s.connections[id]
	if !ok {
		c = &conn{}
		c.mtx.Lock()
		s.connections[id] = c
	}
	s.connectionsMtx.Unlock()
	if ok {
		c.mtx.Lock()
	}
	defer c.mtx.Unlock()
	if c.clientConn == nil {
		conn, err := grpc.Dial(string(target), s.DialOptions...)
		if err != nil {
			return nil, err
		}
		c.clientConn = conn
		c.client = pb.NewRecsClient(conn)
	}
	return c.client, nil
}

func (s *Server) findServerWithLeastWorlds() string {
	// Find server with least amount of assigned worlds
	var min int
	var server *raft.Server
	for _, srv := range s.raft.GetConfiguration().Configuration().Servers {
		if min == 0 || len(s.s2w[string(srv.ID)]) < min {
			min = len(s.s2w[string(srv.ID)])
			server = &srv
		}
	}
	if server == nil {
		return ""
	}
	return string(server.ID)
}

func (s *Server) findServerWithWorld(world string) (pb.RecsClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	server, ok := s.w2s[world]
	if !ok {
		return nil, fmt.Errorf("world %s not found", world)
	}
	for _, n := range s.raft.GetConfiguration().Configuration().Servers {
		if n.ID == raft.ServerID(server) {
			c, err := s.getPeer(n.ID, n.Address)
			if err != nil {
				return nil, err
			}
			return c, nil
		}
	}
	return nil, fmt.Errorf("server owning world %s not found", world)
}

func (s *Server) Subscribe(ctx context.Context, topics ...string) *chan []events.Message {
	return s.Broker.Subscribe(ctx, topics...)
}

func (s *Server) Unsubscribe(ctx context.Context, sub *chan []events.Message, topics ...string) {
	s.Broker.Unsubscribe(ctx, sub, topics...)
}

func (s *Server) Publish(ctx context.Context, topic string, message []events.Message) error {
	m := make([]*pb.Message, len(message))
	for i, msg := range message {
		m[i] = &pb.Message{
			Idx:    msg.Idx,
			Op:     pb.Op(msg.Op),
			Entity: msg.Entity,
		}
		if msg.Key != "" {
			m[i].Key = &msg.Key
		}
		if msg.Value != nil {
			v, err := json.Marshal(msg.Value)
			if err != nil {
				return err
			}
			m[i].Value = v
		}
	}
	config := s.raft.GetConfiguration()
	servers := config.Configuration().Servers
	var wg sync.WaitGroup
	wg.Add(len(servers))
	for _, srv := range servers {
		go func(id raft.ServerID, address raft.ServerAddress) {
			c, err := s.getPeer(id, address)
			if err == nil {
				c.Publish(
					ctx,
					&pb.Event{Topic: topic, Messages: m},
					grpc.UseCompressor(gzip.Name),
				)
			} else {
				log.Printf("failed to publish to %s: %s", address, err)
			}
			wg.Done()
		}(srv.ID, srv.Address)
	}
	wg.Wait()
	return nil
}

func (s *Server) CreateWorld(ctx context.Context, name string) error {
	if s.raft.State() != raft.Leader {
		return fmt.Errorf("not leader")
	}
	serverID := s.findServerWithLeastWorlds()
	if serverID == "" {
		return fmt.Errorf("no servers available")
	}
	b, err := json.Marshal(&command{Op: "new", Key: serverID, Value: name})
	if err != nil {
		return err
	}
	return s.raft.Apply(b, raftTimeout).Error()
}

func (s *Server) DeleteWorld(ctx context.Context, name string) error {
	if s.raft.State() != raft.Leader {
		return fmt.Errorf("not leader")
	}
	b, err := json.Marshal(&command{Op: "del", Key: name})
	if err != nil {
		return err
	}
	return s.raft.Apply(b, raftTimeout).Error()
}

func (s *Server) Create(ctx context.Context, world string, components map[string]interface{}) (uint64, error) {
	newComponents := map[string][]byte{}
	for k, v := range components {
		val, err := json.Marshal(v)
		if err != nil {
			return 0, err
		}
		newComponents[k] = val
	}
	s.mu.Lock()
	if _, ok := s.w2s[world]; !ok {
		s.logger.Printf("world %s not found, creating", world)
		s.mu.Unlock()
		err := s.CreateWorld(ctx, world)
		if err != nil {
			s.logger.Printf("failed to create world: %s", err)
			return 0, err
		}
		s.mu.Lock()
	}
	s.mu.Unlock()
	c, err := s.findServerWithWorld(world)
	if err != nil {
		return 0, err
	}
	resp, err := c.Create(
		ctx,
		&pb.CreateEntity{World: world, Components: newComponents},
		grpc.UseCompressor(gzip.Name),
	)
	if err != nil {
		return 0, err
	}
	return resp.Id, nil
}

func (s *Server) Delete(ctx context.Context, world string, id uint64) error {
	c, err := s.findServerWithWorld(world)
	if err != nil {
		return err
	}
	_, err = c.Delete(ctx, &pb.DeleteEntity{World: world, Entity: &pb.Entity{Id: id}})
	return err
}

func (s *Server) Set(ctx context.Context, world string, id uint64, component string, value string) error {
	c, err := s.findServerWithWorld(world)
	if err != nil {
		return err
	}
	_, err = c.Set(
		ctx,
		&pb.SetComponent{
			World:     world,
			Entity:    &pb.Entity{Id: id},
			Component: component,
			Value:     &pb.Component{Value: []byte(value)},
		},
		grpc.UseCompressor(gzip.Name),
	)
	return err
}

func (s *Server) Remove(ctx context.Context, world string, id uint64, component string) error {
	c, err := s.findServerWithWorld(world)
	if err != nil {
		return err
	}
	_, err = c.Remove(
		ctx,
		&pb.RemoveComponent{World: world, Entity: &pb.Entity{Id: id}, Component: component},
	)
	return err
}

func (s *Server) Move(ctx context.Context, from string, to string, copy bool, entities ...uint64) ([]uint64, error) {
	c, err := s.findServerWithWorld(from)
	if err != nil {
		return nil, err
	}
	resp, err := c.Move(
		ctx,
		&pb.MoveEntity{From: from, To: to, Copy: copy, Entities: entities},
		grpc.UseCompressor(gzip.Name),
	)
	return resp.Entities, err
}

func (s *Server) Get(ctx context.Context, world string, id uint64, component string, yield func(uint64, uint64, []byte) bool) error {
	c, err := s.findServerWithWorld(world)
	if err != nil {
		return err
	}
	resp, err := c.Get(
		ctx,
		&pb.GetEntity{World: world, Entity: &pb.Entity{Id: id}, Component: component},
		grpc.UseCompressor(gzip.Name),
	)
	if err != nil {
		return err
	}
	var idx uint64
	var eid uint64
	for {
		ent, err := resp.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if ent.Commit != nil {
			idx = *ent.Commit
		} else {
			idx = 0
		}
		if ent.Entity != nil {
			eid = *ent.Entity
		} else {
			eid = 0
		}
		if !yield(idx, eid, ent.Value) {
			return nil
		}
	}
}

func (f *fsm) Apply(l *raft.Log) interface{} {
	var c command
	if err := json.Unmarshal(l.Data, &c); err != nil {
		panic(fmt.Sprintf("failed to unmarshal command: %s", err.Error()))
	}
	switch c.Op {
	case "new":
		return f.applyNewWorld(c.Key, c.Value)
	case "del":
		return f.applyDelWorld(c.Key)
	default:
		panic(fmt.Sprintf("unrecognized command op: %s", c.Op))
	}
}

func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wm := make(map[string]string)
	for k, v := range f.w2s {
		wm[k] = v
	}
	return &fsmSnapshot{store: wm}, nil
}

func (f *fsm) Restore(rc io.ReadCloser) error {
	o := make(map[string]string)
	if err := json.NewDecoder(rc).Decode(&o); err != nil {
		return err
	}
	f.w2s = o
	f.s2w = make(map[string][]string)
	for k, v := range o {
		f.s2w[v] = append(f.s2w[v], k)
	}
	return nil
}

// Join joins a node, identified by nodeID and located at addr, to this store.
// The node must be ready to respond to Raft communications at that address.
func (s *Server) Join(nodeID, addr string) error {
	s.logger.Printf("received join request for remote node %s at %s", nodeID, addr)

	configFuture := s.raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		s.logger.Printf("failed to get raft configuration: %v", err)
		return err
	}
	for _, srv := range configFuture.Configuration().Servers {
		// If a node already exists with either the joining node's ID or address,
		// that node may need to be removed from the config first.
		if srv.ID == raft.ServerID(nodeID) || srv.Address == raft.ServerAddress(addr) {
			// However if *both* the ID and the address are the same, then nothing -- not even
			// a join operation -- is needed.
			if srv.Address == raft.ServerAddress(addr) && srv.ID == raft.ServerID(nodeID) {
				s.logger.Printf("node %s at %s already member of cluster, ignoring join request", nodeID, addr)
				return nil
			}

			future := s.raft.RemoveServer(srv.ID, 0, 0)
			if err := future.Error(); err != nil {
				return fmt.Errorf("error removing existing node %s at %s: %s", nodeID, addr, err)
			}
		}
	}
	f := s.raft.AddVoter(raft.ServerID(nodeID), raft.ServerAddress(addr), 0, 0)
	if f.Error() != nil {
		return f.Error()
	}
	s.logger.Printf("node %s at %s joined successfully", nodeID, addr)
	return nil
}

func (f *fsm) applyNewWorld(server string, name string) interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.s2w[server] = append(f.s2w[server], name)
	f.w2s[name] = server
	return nil
}

func (f *fsm) applyDelWorld(name string) interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	serverid := f.w2s[name]
	delete(f.w2s, name)
	for i, n := range f.s2w[serverid] {
		if n == name {
			f.s2w[serverid] = append(f.s2w[serverid][:i], f.s2w[serverid][i+1:]...)
			break
		}
	}
	f.wm.Delete(name)
	return nil
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
