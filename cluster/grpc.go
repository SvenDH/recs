package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/SvenDH/recs/events"
	pb "github.com/SvenDH/recs/proto"
	world "github.com/SvenDH/recs/world"
	"google.golang.org/grpc"
)

type grpcApi struct {
	b  events.Broker
	wm *world.WorldManager

	pb.UnsafeRecsServer
}

func Register(s *grpc.Server, b events.Broker, wm *world.WorldManager) {
	pb.RegisterRecsServer(s, grpcApi{b: b, wm: wm})
}

func (s grpcApi) getWorld(ctx context.Context, name string) *world.World {
	w := s.wm.Get(ctx, name)
	if w == nil {
		return s.wm.New(ctx, name)
	}
	return w
}

func (s grpcApi) Publish(ctx context.Context, req *pb.Event) (*pb.Empty, error) {
	newMessages := make([]events.Message, len(req.Messages))
	for _, message := range req.Messages {
		msg := events.Message{
			Idx:     message.Idx,
			Op:      events.Op(message.Op),
			Entity:  message.Entity,
		}
		if message.Key != nil {
			msg.Key = *message.Key
		}
		if message.Value != nil {
			m := interface{}(nil)
			err := json.Unmarshal([]byte(message.Value), &m)
			if err != nil {
				return nil, err
			}
			msg.Value = m
		}
		newMessages = append(newMessages, msg)
	}
	s.b.Publish(ctx, req.Topic, newMessages)
	return &pb.Empty{}, nil
}

func (s grpcApi) CreateWorld(ctx context.Context, req *pb.World) (*pb.Empty, error) {
	s.getWorld(ctx, req.Name)
	return &pb.Empty{}, nil
}
func (s grpcApi) DeleteWorld(ctx context.Context, req *pb.World) (*pb.Empty, error) {
	s.wm.Delete(req.Name)
	return &pb.Empty{}, nil
}

func (s grpcApi) Create(ctx context.Context, req *pb.CreateEntity) (*pb.Entity, error) {
	data := []interface{}{}
	for k, v := range req.Components {
		t, ok := s.wm.Components[k]
		if !ok {
			continue
		}
		c := reflect.New(t.Type).Interface()
		if err := json.Unmarshal(v, c); err != nil {
			return nil, err
		}
		data = append(data, c)
	}
	e, err := s.getWorld(ctx, req.World).New(ctx, data...)
	if err != nil {
		return nil, err
	}
	return &pb.Entity{Id: e}, err
}

func (s grpcApi) Delete(ctx context.Context, req *pb.DeleteEntity) (*pb.Empty, error) {
	return &pb.Empty{}, s.getWorld(ctx, req.World).Delete(ctx, req.Entity.Id)
}

func (s grpcApi) Set(ctx context.Context, req *pb.SetComponent) (*pb.Empty, error) {
	t, ok := s.wm.Components[req.Component]
	if !ok {
		return nil, fmt.Errorf("component %s not found", req.Component)
	}
	d := reflect.New(t.Type)
	if err := json.Unmarshal(req.Value.Value, d.Interface()); err != nil {
		return nil, err
	}
	return &pb.Empty{}, s.getWorld(ctx, req.World).Set(ctx, req.Entity.Id, d.Elem().Interface())
}

func (s grpcApi) Remove(ctx context.Context, req *pb.RemoveComponent) (*pb.Empty, error) {
	return &pb.Empty{}, s.getWorld(ctx, req.World).Remove(ctx, req.Entity.Id, req.Component)
}

func (s grpcApi) Get(req *pb.GetEntity, stream pb.Recs_GetServer) error {
	var ent uint64
	if req.Entity != nil {
		ent = req.Entity.Id
	}
	w := s.getWorld(stream.Context(), req.World)
	if ent == 0 {
		return w.Iter(
			stream.Context(),
			req.Component,
			func(idx uint64, e uint64, v interface{}) error {
				j, err := json.Marshal(v)
				if err != nil {
					return err
				}
				var ip *uint64
				if idx != 0 {
					ip = &idx
				}
				return stream.Send(&pb.Component{Commit: ip, Entity: &e, Value: j})
			},
		)
	}
	idx, d := w.Get(stream.Context(), ent, req.Component)
	j, err := json.Marshal(d)
	if err != nil {
		return err
	}
	var ip *uint64
	if idx != 0 {
		ip = &idx
	}
	return stream.Send(&pb.Component{Commit: ip, Entity: &ent, Value: j})
}

func (s grpcApi) Move(ctx context.Context, req *pb.MoveEntity) (*pb.MoveEntityResponse, error) {
	arr, err := s.getWorld(ctx, req.From).Move(ctx, req.To, req.Copy, req.Entities...)
	return &pb.MoveEntityResponse{Entities: arr}, err
}
