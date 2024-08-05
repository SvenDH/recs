package modules

import (
	"github.com/SvenDH/recs/cluster"
//	"github.com/mlange-42/arche/ecs"
)

type Message string

type Chat struct {
	name string
	users []string
}

func RegisterChat(s *cluster.Server) {
	cluster.RegisterComponent[Message](s, true, true, true)
	cluster.RegisterComponent[Chat](s, true, true, true)
	//cluster.RegisterSystem(s, &Chat{})
}
